const OG = {
  me: null,
  // esc:HTML 文本转义(C-1)。所有 innerHTML 插值里"人输入的内容"(姓名/班级/题干/
  // 复核理由/分组名/显示名…)必须过 esc —— 学生复核理由→教师详情曾是存储型 XSS 链路。
  esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  },
  async init() {
    try { this.me = await this.jget("/api/v1/auth/me"); }
    catch (e) { location.href = "/login.html"; return false; }
    const el = document.getElementById("ogUser");
    if (el) el.innerHTML = `${this.esc(this.me.displayName)}(${this.roleName(this.me.role)}) · <a href="#" onclick="OG.logout();return false">退出</a>`;
    return true;
  },
  roleName(r) { return { admin: "管理员", leader: "年级组长", teacher: "教师", student: "学生", parent: "家长" }[r] || r; },
  is(...roles) { return this.me && roles.includes(this.me.role); },
  // strHash:djb2,32 位循环哈希。给 localStorage 里"版式↔参考答案"配对做指纹:
  // 答案数组没有指纹校验的话,设计器改版式后旧答案会按下标静默套进新题序。
  strHash(s) {
    let h = 5381;
    s = String(s == null ? "" : s);
    for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0;
    return (h >>> 0).toString(36);
  },
  // errMsg:非 2xx 时给可读错误 —— 网关/代理回 HTML 时不再抛 "Unexpected token '<'"
  async errMsg(r) {
    const text = await r.text().catch(() => "");
    try { const j = JSON.parse(text); if (j && j.error) return j.error; } catch (_) { /* 非 JSON 响应 */ }
    if (/<html/i.test(text)) return `HTTP ${r.status}(网关/代理异常,非本服务响应)`;
    return text && text.length <= 120 ? `HTTP ${r.status}:${text}` : `HTTP ${r.status}`;
  },
  async jget(url) { const r = await fetch(url); if (!r.ok) throw new Error(await this.errMsg(r)); return r.json(); },
  async jpost(url, body, method = "POST") {
    const r = await fetch(url, { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (!r.ok) throw new Error(await this.errMsg(r));
    return r.json();
  },
  logout() { this.jpost("/api/v1/auth/logout", {}).then(() => location.href = "/login.html"); },
  // chartLine 零依赖折线图:labels 为 x 轴刻度文本,series 为 [{name,color,values}],
  // values 与 labels 对齐,null 断线;yFmt 格式化 y 轴与悬停数值。
  chartLine(cv, labels, series, yFmt = v => v) {
    if (!cv) return;
    const ctx = cv.getContext("2d");
    const W = cv.width, H = cv.height, padL = 46, padR = 14, padT = 16, padB = 30;
    ctx.clearRect(0, 0, W, H);
    ctx.font = "11px sans-serif";
    const vals = series.flatMap(s => s.values.filter(v => v != null));
    let yMax = Math.max(...vals, 0), yMin = Math.min(...vals, 0);
    if (yMax === yMin) { yMax += 1; yMin -= 1; }
    const pad = (yMax - yMin) * 0.1; yMax += pad; if (yMin - pad < 0 && yMin >= 0) yMin = Math.max(0, yMin - pad * 0.5); else yMin -= pad;
    const X = i => padL + (labels.length <= 1 ? 0 : (W - padL - padR) * i / (labels.length - 1));
    const Y = v => padT + (H - padT - padB) * (1 - (v - yMin) / (yMax - yMin));
    // 网格与 y 刻度
    ctx.textAlign = "right"; ctx.fillStyle = "#8a94a3";
    for (let g = 0; g <= 4; g++) {
      const v = yMin + (yMax - yMin) * g / 4, y = Y(v);
      ctx.strokeStyle = "#eef2f6"; ctx.beginPath(); ctx.moveTo(padL, y); ctx.lineTo(W - padR, y); ctx.stroke();
      ctx.fillText(yFmt(v), padL - 5, y + 4);
    }
    // x 刻度(过密时隔一显示)
    ctx.textAlign = "center"; ctx.fillStyle = "#8a94a3";
    const step = labels.length > 8 ? Math.ceil(labels.length / 8) : 1;
    labels.forEach((t, i) => { if (i % step === 0) ctx.fillText(t, X(i), H - 10); });
    // 折线与数据点
    for (const s of series) {
      ctx.strokeStyle = s.color; ctx.lineWidth = 2; ctx.beginPath();
      let pen = false;
      s.values.forEach((v, i) => {
        if (v == null) { pen = false; return; }
        const x = X(i), y = Y(v);
        if (!pen) { ctx.moveTo(x, y); pen = true; } else ctx.lineTo(x, y);
      });
      ctx.stroke();
      s.values.forEach((v, i) => {
        if (v == null) return;
        ctx.fillStyle = s.color; ctx.beginPath(); ctx.arc(X(i), Y(v), 3, 0, 7); ctx.fill();
      });
    }
    // 图例
    ctx.textAlign = "left"; let lx = padL;
    for (const s of series) {
      ctx.fillStyle = s.color; ctx.fillRect(lx, 4, 10, 4);
      ctx.fillStyle = "#66707d"; ctx.fillText(s.name, lx + 14, 9);
      lx += 14 + ctx.measureText(s.name).width + 18;
    }
  },

  // —— PDF → 图像(多页扫描件拆页;设计器「从试卷识别」与工作台「扫描上传」共用)——
  // PDF 用本地 pdf.js(web/vendor,离线可用)逐页渲染成 JPEG。
  pdfjsReady: null,
  ensurePdfJs() {
    if (this.pdfjsReady) return this.pdfjsReady;
    this.pdfjsReady = new Promise((resolve, reject) => {
      const s = document.createElement("script");
      s.src = "/vendor/pdf.min.js";
      s.onload = () => {
        window.pdfjsLib.GlobalWorkerOptions.workerSrc = "/vendor/pdf.worker.min.js";
        resolve(window.pdfjsLib);
      };
      s.onerror = () => reject(new Error("pdf.js 加载失败(web/vendor/pdf.min.js 缺失)"));
      document.head.appendChild(s);
    });
    return this.pdfjsReady;
  },
  async pdfToImages(file, onProgress) {
    const lib = await this.ensurePdfJs();
    const buf = await file.arrayBuffer();
    const pdf = await lib.getDocument({ data: buf }).promise;
    const out = [];
    for (let p = 1; p <= pdf.numPages; p++) {
      const page = await pdf.getPage(p);
      const base = page.getViewport({ scale: 1 });
      const scale = Math.min(4, Math.max(1.2, 2200 / base.width)); // 目标宽 ~2200px(≈265dpi):PDF 内嵌 300dpi 扫描图少降采样,二维码考号模块保 6px+
      const vp = page.getViewport({ scale });
      const cv = document.createElement("canvas");
      cv.width = Math.floor(vp.width); cv.height = Math.floor(vp.height);
      await page.render({ canvasContext: cv.getContext("2d"), viewport: vp }).promise;
      const blob = await new Promise(res => cv.toBlob(res, "image/jpeg", 0.85));
      out.push(new File([blob], file.name.replace(/\.pdf$/i, "") + `-p${p}.jpg`, { type: "image/jpeg" }));
      if (onProgress) onProgress(p, pdf.numPages);
    }
    return out;
  },
  // collectScanFiles 把用户选的文件统一成图像数组:PDF 拆页,图片直通。msg 可为 null。
  async collectScanFiles(files, msg) {
    const images = [];
    for (const f of files) {
      if (/\.pdf$/i.test(f.name) || f.type === "application/pdf") {
        try {
          const imgs = await this.pdfToImages(f, (p, n) => { if (msg) msg.innerHTML = `<span class="hint">正在拆页 ${OG.esc(f.name)}:${p}/${n} 页…</span>`; });
          images.push(...imgs);
        } catch (e) {
          throw new Error(`PDF ${f.name} 拆页失败:${e.message}`);
        }
      } else {
        images.push(f);
      }
    }
    return images;
  },
};
