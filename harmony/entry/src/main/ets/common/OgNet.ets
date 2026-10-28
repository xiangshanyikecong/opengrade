// 统一 HTTP 客户端:Bearer 鉴权、{error} 文案映射、401 全局登出、
// multipart 上传(先拷入沙箱再上传)、二进制下载与沙箱落盘、鉴权位图缓存。

import { http } from '@kit.NetworkKit';
import { router } from '@kit.ArkUI';
import { picker } from '@kit.CoreFileKit';
import { fs } from '@kit.CoreFileKit';
import { image } from '@kit.ImageKit';
import { util } from '@kit.ArkTS';
import { OgSession } from './OgSession';
import { LoginResp } from '../model/Api';

export class OgError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export interface UploadFile {
  field: string; // 表单字段名(如 images)
  fileName: string;
  contentType: string; // image/jpeg
  filePath: string; // 沙箱内路径
}

interface ReqOpts {
  method: string;
  path: string; // 以 /api/v1 开头,或绝对 URL
  body?: string; // 已序列化 JSON
  contentType?: string;
  timeout?: number;
  rawArrayBuffer?: boolean;
}

export class OgNet {
  // 401 统一处理:清会话回登录页(登录页自身不带 token,不会触发)。
  private static onUnauthorized(): void {
    if (OgSession.token() !== '') {
      OgSession.clearToken();
      router.replaceUrl({ url: 'pages/LoginPage' }).catch(() => {});
    }
  }

  private static url(path: string): string {
    if (path.startsWith('http://') || path.startsWith('https://')) {
      return path;
    }
    return OgSession.baseUrl() + path;
  }

  private static authHeaders(): Record<string, string> {
    const h: Record<string, string> = {};
    const tok = OgSession.token();
    if (tok !== '') {
      h['Authorization'] = 'Bearer ' + tok;
    }
    return h;
  }

  static errText(status: number, body: string): string {
    if (body !== '') {
      try {
        const m = JSON.parse(body) as Record<string, string>;
        if (typeof m['error'] === 'string' && m['error'] !== '') {
          return m['error'];
        }
      } catch (e) {
        // 非 JSON 响应(网关错误页等)回落到状态码文案
      }
    }
    if (status === 401) {
      return '未登录或会话已过期';
    }
    if (status === 403) {
      return '没有权限执行此操作';
    }
    if (status === 409) {
      return '刚被他人改过,请刷新后重试';
    }
    if (status === 429) {
      return '尝试过于频繁,请稍后再试';
    }
    if (status >= 500) {
      return '服务端错误(' + status + ')';
    }
    return '请求失败(HTTP ' + status + ')';
  }

  private static async req(o: ReqOpts): Promise<string | ArrayBuffer> {
    if (OgSession.baseUrl() === '' && !o.path.startsWith('http')) {
      throw new OgError(0, '尚未配置服务器地址');
    }
    const hr = http.createHttp();
    try {
      const headers: Record<string, string> = OgNet.authHeaders();
      headers['Content-Type'] = o.contentType ?? 'application/json';
      const opt: http.HttpRequestOptions = {
        method: o.method as http.RequestMethod,
        header: headers,
        expectDataType: o.rawArrayBuffer ? http.HttpDataType.ARRAY_BUFFER : http.HttpDataType.STRING,
        connectTimeout: 10000,
        readTimeout: o.timeout ?? 30000
      };
      if (o.body !== undefined && o.body !== '') {
        opt.extraData = o.body;
      }
      const resp = await hr.request(OgNet.url(o.path), opt);
      if (resp.responseCode >= 200 && resp.responseCode < 300) {
        if (o.rawArrayBuffer) {
          return resp.result as ArrayBuffer;
        }
        return resp.result as string;
      }
      const bodyText = typeof resp.result === 'string' ? resp.result : '';
      if (resp.responseCode === 401) {
        OgNet.onUnauthorized();
      }
      throw new OgError(resp.responseCode, OgNet.errText(resp.responseCode, bodyText));
    } catch (e) {
      if (e instanceof OgError) {
        throw e;
      }
      throw new OgError(0, '网络异常:无法连接服务器');
    } finally {
      hr.destroy();
    }
  }

  static async jget<T>(path: string, timeout?: number): Promise<T> {
    const text = await OgNet.req({ method: 'GET', path: path, timeout: timeout }) as string;
    return JSON.parse(text) as T;
  }

  static async jpost<T>(path: string, body: Object | null): Promise<T> {
    const text = await OgNet.req({
      method: 'POST', path: path,
      body: body === null ? '{}' : JSON.stringify(body)
    }) as string;
    if (text === '') {
      return {} as T;
    }
    return JSON.parse(text) as T;
  }

  static async jput<T>(path: string, body: Object | null): Promise<T> {
    const text = await OgNet.req({
      method: 'PUT', path: path,
      body: body === null ? '{}' : JSON.stringify(body)
    }) as string;
    if (text === '') {
      return {} as T;
    }
    return JSON.parse(text) as T;
  }

  static async jpatch<T>(path: string, body: Object): Promise<T> {
    const text = await OgNet.req({ method: 'PATCH', path: path, body: JSON.stringify(body) }) as string;
    if (text === '') {
      return {} as T;
    }
    return JSON.parse(text) as T;
  }

  static async jdel<T>(path: string): Promise<T> {
    const text = await OgNet.req({ method: 'DELETE', path: path }) as string;
    if (text === '') {
      return {} as T;
    }
    return JSON.parse(text) as T;
  }

  // 服务器连通性校验(登录页用):返回版本号。
  static async checkServer(base: string): Promise<string> {
    const hr = http.createHttp();
    try {
      const opt: http.HttpRequestOptions = {
        method: http.RequestMethod.GET,
        expectDataType: http.HttpDataType.STRING,
        connectTimeout: 5000,
        readTimeout: 5000
      };
      const resp = await hr.request(base.replace(/\/+$/, '') + '/healthz', opt);
      if (resp.responseCode !== 200) {
        throw new OgError(resp.responseCode, '服务器返回 HTTP ' + resp.responseCode);
      }
      const m = JSON.parse(resp.result as string) as Record<string, string>;
      if (m['service'] !== 'opengrade') {
        throw new OgError(0, '这不是 OpenGrade 服务器');
      }
      return m['version'] ?? '';
    } catch (e) {
      if (e instanceof OgError) {
        throw e;
      }
      throw new OgError(0, '无法连接服务器,请检查地址与网络');
    } finally {
      hr.destroy();
    }
  }

  // 原生登录:native:true → 响应携带 token。
  static async nativeLogin(base: string, username: string, password: string): Promise<LoginResp> {
    const hr = http.createHttp();
    try {
      const opt: http.HttpRequestOptions = {
        method: http.RequestMethod.POST,
        header: { 'Content-Type': 'application/json' } as Record<string, string>,
        extraData: JSON.stringify({ 'username': username, 'password': password, 'native': true }) as string,
        expectDataType: http.HttpDataType.STRING,
        connectTimeout: 10000,
        readTimeout: 15000
      };
      const resp = await hr.request(base.replace(/\/+$/, '') + '/api/v1/auth/login', opt);
      const text = resp.result as string;
      if (resp.responseCode !== 200) {
        throw new OgError(resp.responseCode, OgNet.errText(resp.responseCode, text));
      }
      return JSON.parse(text) as LoginResp;
    } catch (e) {
      if (e instanceof OgError) {
        throw e;
      }
      throw new OgError(0, '网络异常:无法连接服务器');
    } finally {
      hr.destroy();
    }
  }

  static async getBytes(path: string, timeout?: number): Promise<ArrayBuffer> {
    return await OgNet.req({
      method: 'GET', path: path, rawArrayBuffer: true,
      timeout: timeout ?? 120000
    }) as ArrayBuffer;
  }

  // multipart 上传:文本字段 + 文件(文件须已在沙箱内,见 copyUriToCache)。
  static async upload(path: string, fields: Record<string, string>, files: UploadFile[], timeout?: number): Promise<string> {
    if (OgSession.baseUrl() === '') {
      throw new OgError(0, '尚未配置服务器地址');
    }
    const hr = http.createHttp();
    try {
      const forms: Array<http.MultiFormData> = [];
      const names = Object.keys(fields);
      for (const k of names) {
        const f: http.MultiFormData = {
          name: k,
          contentType: 'text/plain',
          data: fields[k]
        };
        forms.push(f);
      }
      for (const f of files) {
        const mf: http.MultiFormData = {
          name: f.field,
          contentType: f.contentType,
          remoteFileName: f.fileName,
          filePath: f.filePath
        };
        forms.push(mf);
      }
      const headers: Record<string, string> = OgNet.authHeaders();
      const opt: http.HttpRequestOptions = {
        method: http.RequestMethod.POST,
        header: headers,
        multiFormDataList: forms,
        expectDataType: http.HttpDataType.STRING,
        connectTimeout: 10000,
        readTimeout: timeout ?? 600000
      };
      const resp = await hr.request(OgNet.url(path), opt);
      if (resp.responseCode >= 200 && resp.responseCode < 300) {
        return resp.result as string;
      }
      const bodyText = typeof resp.result === 'string' ? resp.result : '';
      if (resp.responseCode === 401) {
        OgNet.onUnauthorized();
      }
      throw new OgError(resp.responseCode, OgNet.errText(resp.responseCode, bodyText));
    } catch (e) {
      if (e instanceof OgError) {
        throw e;
      }
      throw new OgError(0, '上传失败:网络异常或文件不可读');
    } finally {
      hr.destroy();
    }
  }

  // 把 picker 返回的 file://media URI 拷入沙箱缓存,返回沙箱路径(multipart filePath 需要真实路径)。
  static async copyUriToCache(uri: string, cacheName: string): Promise<string> {
    const ctx = OgSession.context();
    const dest = ctx.cacheDir + '/' + cacheName;
    const src = await fs.open(uri, fs.OpenMode.READ_ONLY);
    try {
      await fs.copyFile(src.fd, dest);
    } finally {
      await fs.close(src.fd);
    }
    return dest;
  }

  // 读取 picker 文件为文本(版式 JSON / CSV 粘贴导入兜底)。
  static async readUriText(uri: string): Promise<string> {
    const f = await fs.open(uri, fs.OpenMode.READ_ONLY);
    try {
      const st = await fs.stat(f.fd);
      const buf = new ArrayBuffer(st.size);
      fs.readSync(f.fd, buf);
      return OgNet.bufToText(buf);
    } finally {
      await fs.close(f.fd);
    }
  }

  // 下载二进制并通过系统「另存为」落盘(成绩册/数据包/成绩单等)。
  static async downloadToPicker(path: string, fileName: string): Promise<string> {
    const buf = await OgNet.getBytes(path, 600000);
    const dp = new picker.DocumentViewPicker();
    const opt: picker.DocumentSaveOptions = { newFileNames: [fileName] };
    const uris = await dp.save(opt);
    if (uris.length === 0) {
      return '';
    }
    const f = await fs.open(uris[0], fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
    try {
      await fs.write(f.fd, buf);
    } finally {
      await fs.close(f.fd);
    }
    return uris[0];
  }

  static bufToText(buf: ArrayBuffer): string {
    const decoder = util.TextDecoder.create('utf-8');
    return decoder.decodeToString(new Uint8Array(buf));
  }

  static qs(params: Record<string, string>): string {
    const keys = Object.keys(params);
    const parts: string[] = [];
    for (const k of keys) {
      if (params[k] !== '') {
        parts.push(encodeURIComponent(k) + '=' + encodeURIComponent(params[k]));
      }
    }
    if (parts.length === 0) {
      return '';
    }
    return '?' + parts.join('&');
  }
}

// 鉴权位图内存缓存:OgImage 组件与 Grader 预加载共用;FIFO 淘汰。
export class ImageCache {
  private static map: Map<string, image.PixelMap> = new Map();
  private static order: string[] = [];
  static readonly CAP: number = 80;

  static get(key: string): image.PixelMap | null {
    return ImageCache.map.get(key) ?? null;
  }

  static put(key: string, pm: image.PixelMap): void {
    if (ImageCache.map.has(key)) {
      return;
    }
    ImageCache.map.set(key, pm);
    ImageCache.order.push(key);
    while (ImageCache.order.length > ImageCache.CAP) {
      const old = ImageCache.order.shift();
      if (old !== undefined) {
        ImageCache.map.delete(old);
      }
    }
  }

  static has(key: string): boolean {
    return ImageCache.map.has(key);
  }

  // 预取:下载 + 解码入缓存(失败静默,交由正式加载重试)。
  static async prefetch(url: string, maxDim: number): Promise<void> {
    if (ImageCache.map.has(url)) {
      return;
    }
    try {
      const pm = await OgNet.decodeImageUrl(url, maxDim);
      ImageCache.put(url, pm);
    } catch (e) {
      // 静默:预加载失败不打扰用户
    }
  }
}

// 下载并解码为 PixelMap(可选降采样上限,防止大图占满内存)。
export class OgImageCodec {
  static async decodeBuf(buf: ArrayBuffer, maxDim: number): Promise<image.PixelMap> {
    const src = image.createImageSource(buf);
    try {
      const info = await src.getImageInfo();
      const opts: image.DecodingOptions = {};
      const w = info.size.width;
      const h = info.size.height;
      if (maxDim > 0 && (w > maxDim || h > maxDim)) {
        const k = maxDim / Math.max(w, h);
        opts.desiredSize = { width: Math.round(w * k), height: Math.round(h * k) };
      }
      return await src.createPixelMap(opts);
    } finally {
      await src.release();
    }
  }

  static async decodeUrl(url: string, maxDim: number): Promise<image.PixelMap> {
    const buf = await OgNet.getBytes(url);
    return await OgImageCodec.decodeBuf(buf, maxDim);
  }
}
