const OG = {
  me: null,
  async init() {
    try { this.me = await this.jget("/api/v1/auth/me"); }
    catch (e) { location.href = "/login.html"; return false; }
    const el = document.getElementById("ogUser");
    if (el) el.innerHTML = `${this.me.displayName}(${this.roleName(this.me.role)}) · <a href="#" onclick="OG.logout();return false">退出</a>`;
    return true;
  },
  roleName(r) { return { admin: "管理员", leader: "年级组长", teacher: "教师", student: "学生", parent: "家长" }[r] || r; },
  is(...roles) { return this.me && roles.includes(this.me.role); },
  async jget(url) { const r = await fetch(url); if (!r.ok) throw new Error((await r.json()).error || r.status); return r.json(); },
  async jpost(url, body, method = "POST") {
    const r = await fetch(url, { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (!r.ok) throw new Error((await r.json()).error || r.status);
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
};
