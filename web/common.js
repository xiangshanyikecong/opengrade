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

  // —— 全站轻提示(抽自阅卷工作台的原地实现;页面不再各写一套)——
  toast(msg, ms = 2600) {
    let box = document.getElementById("ogToasts");
    if (!box) {
      box = document.createElement("div");
      box.id = "ogToasts";
      document.body.appendChild(box);
    }
    const t = document.createElement("div");
    t.className = "og-toast";
    t.textContent = msg;
    box.appendChild(t);
    setTimeout(() => { t.classList.add("out"); setTimeout(() => t.remove(), 260); }, ms);
  },

  // —— 列表套件:chips 过滤 + 防抖搜索 + 分页(每页 10 条)——
  // 大列表(异常清单/复核申请/作废卷…)全量渲染的通病:翻不到、找不着、已处理的插在待处理中间。
  // 套件把"过滤条件 + 搜索词 + 页码"存进 _listStates(id 键),详情页整页重渲染后状态不丢。
  _listStates: {},
  _listKits: {},
  _listTimers: {},
  listState(id) { return this._listStates[id] || (this._listStates[id] = { chip: "", q: "", page: 1 }); },
  // opts:{ id, state, rows:()=>[], chipOf:(row)=>k, chips:[{k,label,count}], search:(row,qLower)=>bool,
  //        placeholder, headHTML:()=>`<tr>…`, rowHTML:(row)=>`<tr>…`, empty, size=10 }
  listKit(opts) {
    const st = opts.state, size = opts.size || 10;
    const kit = {
      filtered() {
        const q = st.q.trim().toLowerCase();
        return opts.rows().filter(r => (!st.chip || opts.chipOf(r) === st.chip) && (!q || opts.search(r, q)));
      },
      render() {
        const host = document.getElementById(opts.id);
        if (!host) return;
        const rows = this.filtered();
        const pages = Math.max(1, Math.ceil(rows.length / size));
        if (st.page > pages) st.page = pages;
        const pg = rows.slice((st.page - 1) * size, st.page * size);
        const chips = (opts.chips || []).map(c =>
          `<button class="chip ${st.chip === c.k && "on"}" onclick="OG._listChip('${opts.id}','${c.k}')">${OG.esc(c.label)}${c.count != null ? ` <b>${c.count}</b>` : ""}</button>`).join("");
        const search = opts.placeholder ? `<div class="list-search"><input id="${opts.id}-q" placeholder="${OG.esc(opts.placeholder)}"
          value="${OG.esc(st.q)}" oninput="OG._listQ('${opts.id}',this.value)"><span class="s-ico">⌕</span></div>` : "";
        const body = pg.length
          ? `<table>${opts.headHTML()}${pg.map(r => opts.rowHTML(r)).join("")}</table>`
          : `<div class="empty" style="padding:24px 0"><div class="pic">🗒️</div>${OG.esc(opts.empty || "暂无数据")}</div>`;
        host.innerHTML = `<div class="list-bar"><div class="chips">${chips}</div>${search}</div>${body}
          ${rows.length > size ? OG.pagerHTML(opts.id, rows.length, st.page, size)
            : `<div class="hint" style="padding:6px 2px">共 ${rows.length} 条</div>`}`;
      },
    };
    this._listKits[opts.id] = kit;
    return kit;
  },
  _listChip(id, k) {
    const st = this.listState(id);
    st.chip = st.chip === k ? "" : k;
    st.page = 1;
    this._listKits[id].render();
  },
  _listQ(id, v) {
    const st = this.listState(id);
    clearTimeout(this._listTimers[id]);
    this._listTimers[id] = setTimeout(() => {
      st.q = v; st.page = 1;
      this._listKits[id].render();
      const inp = document.getElementById(id + "-q");
      if (inp) { inp.focus(); inp.setSelectionRange(v.length, v.length); } // 重渲染后光标回输入框末尾
    }, 250);
  },
  pagerHTML(id, total, page, size) {
    const pages = Math.max(1, Math.ceil(total / size));
    const btn = (p, label, dis, cur) => `<button class="pg-btn ${cur ? "cur" : ""}" ${dis ? "disabled" : ""}
      onclick="OG._listPage('${id}',${p})">${label}</button>`;
    const ns = [];
    for (let p = 1; p <= pages; p++) {
      if (p === 1 || p === pages || Math.abs(p - page) <= 2) ns.push(p);
      else if (ns[ns.length - 1] !== "…") ns.push("…");
    }
    return `<div class="pager"><span class="hint">共 ${total} 条 · 第 ${page}/${pages} 页</span>
      ${btn(page - 1, "‹", page <= 1)}
      ${ns.map(p => p === "…" ? `<span class="pg-ell">…</span>` : btn(p, p, false, p === page)).join("")}
      ${btn(page + 1, "›", page >= pages)}</div>`;
  },
  _listPage(id, p) {
    const st = this.listState(id);
    st.page = p;
    this._listKits[id].render();
  },

  // —— 全屏看图(切片/整卷点击放大)——
  lightbox(src, caption) {
    let ov = document.getElementById("ogLightbox");
    if (!ov) {
      ov = document.createElement("div");
      ov.id = "ogLightbox";
      ov.onclick = () => ov.classList.remove("show");
      document.body.appendChild(ov);
    }
    ov.innerHTML = `<div class="lb-cap">${OG.esc(caption || "")} <span style="opacity:.6">点击任意处关闭</span></div><img src="${OG.esc(src)}" alt="">`;
    ov.classList.add("show");
  },
};
