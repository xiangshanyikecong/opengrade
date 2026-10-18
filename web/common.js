const OG = {
  me: null,
  async init() {
    try { this.me = await this.jget("/api/v1/auth/me"); }
    catch (e) { location.href = "/login.html"; return false; }
    const el = document.getElementById("ogUser");
    if (el) el.innerHTML = `${this.me.displayName}(${this.roleName(this.me.role)}) · <a href="#" onclick="OG.logout();return false">退出</a>`;
    return true;
  },
  roleName(r) { return { admin: "管理员", leader: "年级组长", teacher: "教师", student: "学生" }[r] || r; },
  is(...roles) { return this.me && roles.includes(this.me.role); },
  async jget(url) { const r = await fetch(url); if (!r.ok) throw new Error((await r.json()).error || r.status); return r.json(); },
  async jpost(url, body, method = "POST") {
    const r = await fetch(url, { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    if (!r.ok) throw new Error((await r.json()).error || r.status);
    return r.json();
  },
  logout() { this.jpost("/api/v1/auth/logout", {}).then(() => location.href = "/login.html"); },
};
