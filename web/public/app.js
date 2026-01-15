const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => Array.from(document.querySelectorAll(sel));

// 登录态：JWT 保存在 localStorage（刷新不掉线，默认 30 天过期）
const TOKEN_STORAGE_KEY = "mihomo-pool-token";
let currentToken = null;
try {
  currentToken = localStorage.getItem(TOKEN_STORAGE_KEY);
} catch {
  currentToken = null;
}
const getToken = () => currentToken;
const setToken = (token) => {
  currentToken = token;
  try {
    localStorage.setItem(TOKEN_STORAGE_KEY, token);
  } catch {
    // ignore
  }
};
const clearToken = () => {
  currentToken = null;
  try {
    localStorage.removeItem(TOKEN_STORAGE_KEY);
  } catch {
    // ignore
  }
};

// 检测中的实例 ID 集合（用于在列表中显示"检测中"状态）
const checkingInstances = new Set();

function escapeHtml(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => {
    if (ch === "&") return "&amp;";
    if (ch === "<") return "&lt;";
    if (ch === ">") return "&gt;";
    if (ch === '"') return "&quot;";
    return "&#39;";
  });
}

function closeModal() {
  const modal = $("#modal");
  if (!modal) return;
  modal.classList.add("hidden");
  document.body.style.overflow = "";
  $("#modalActions").innerHTML = "";
  $("#modalTitle").textContent = "";
  $("#modalBody").innerHTML = "";
}

function openModal({ title, bodyHtml, actionsHtml = "" }) {
  const modal = $("#modal");
  if (!modal) return;
  $("#modalTitle").textContent = title || "";
  $("#modalActions").innerHTML = actionsHtml;
  $("#modalBody").innerHTML = bodyHtml;
  modal.classList.remove("hidden");
  document.body.style.overflow = "hidden";
  $("#modalClose")?.focus?.();
}

function openFormModal({ title, bodyHtml, submitText = "保存", onSubmit }) {
  openModal({
    title,
    bodyHtml,
    actionsHtml: `
      <button class="btn sm" id="modalCancel" type="button">取消</button>
      <button class="btn primary sm" id="modalSubmit" type="button">${escapeHtml(submitText)}</button>
    `
  });
  $("#modalCancel")?.addEventListener?.("click", closeModal);
  $("#modalSubmit")?.addEventListener?.("click", async () => {
    const btn = $("#modalSubmit");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "保存中...";
    try {
      await onSubmit?.();
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });
}

(() => {
  $("#modalClose")?.addEventListener?.("click", closeModal);
  $("#modal")?.addEventListener?.("click", (e) => {
    if (e.target === $("#modal")) closeModal();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closeModal();
  });
})();

function toast(msg, ok = true) {
  const t = $("#toast");
  t.textContent = msg;
  t.classList.remove("hidden");
  t.style.borderColor = ok ? "rgba(67, 209, 158, 0.35)" : "rgba(255, 106, 106, 0.35)";
  setTimeout(() => t.classList.add("hidden"), 2200);
}

async function copyToClipboard(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // fallthrough
  }

  try {
    const ta = document.createElement("textarea");
    ta.value = String(text ?? "");
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    ta.style.top = "0";
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return !!ok;
  } catch {
    return false;
  }
}

async function api(path, opts = {}) {
  const token = getToken();
  const headers = { "content-type": "application/json", ...(opts.headers || {}) };
  if (token) headers["authorization"] = `Bearer ${token}`;

  const res = await fetch(path, {
    headers,
    ...opts
  });
  const data = await res.json().catch(() => ({}));
  if (res.status === 401) {
    clearToken();
    throw new Error("未登录或登录已失效，请重新登录");
  }
  if (!res.ok || data.ok === false) {
    throw new Error(data.error || `HTTP ${res.status}`);
  }
  return data;
}

// 后台检测实例可用性（启动后自动调用），不阻塞主流程
async function checkInstanceInBackground(instanceId) {
  checkingInstances.add(instanceId);
  // 重新渲染以显示"检测中"状态
  render();
  try {
    const { health } = await api(`/api/instances/${instanceId}/check`, { method: "POST", body: "{}" });
    if (health?.ok) {
      const ms = typeof health.latencyMs === "number" ? `${Math.round(health.latencyMs)}ms` : "-";
      toast(`节点可用 · ${ms}`);
    } else {
      toast(`节点不可用：${health?.error || "检测失败"}`, false);
    }
  } catch (e) {
    toast(`检测失败：${e.message}`, false);
  } finally {
    checkingInstances.delete(instanceId);
    render();
  }
}

function fmtDate(s) {
  try {
    return new Date(s).toLocaleString();
  } catch {
    return s;
  }
}

function pageHeader(title, subtitle, actionsHtml = "") {
  return `
    <div class="page-header">
      <div>
        <div class="page-title">${escapeHtml(title)}</div>
        <div class="page-subtitle">${escapeHtml(subtitle)}</div>
      </div>
      <div class="panel-actions">${actionsHtml}</div>
    </div>
  `;
}

function setTab(name) {
  $$(".tab").forEach((b) => b.classList.toggle("active", b.dataset.tab === name));
  $$(".view").forEach((v) => v.classList.add("hidden"));
  $(`#view-${name}`).classList.remove("hidden");
  render();
}

$$(".tab").forEach((b) => b.addEventListener("click", () => setTab(b.dataset.tab)));

function setNavVisible(visible) {
  const nav = document.querySelector(".nav");
  if (!nav) return;
  nav.style.display = visible ? "flex" : "none";
}

function renderLogin() {
  closeModal();
  setNavVisible(false);
  $$(".view").forEach((v) => v.classList.add("hidden"));

  const el = $("#view-instances");
  el.classList.remove("hidden");
  el.innerHTML = `
    <div class="login-shell">
      <div class="panel login-card">
        <div class="login-brand">
          <div class="login-logo">mihomo-pool</div>
          <div class="login-tag">多实例代理池管理</div>
        </div>
        <div class="login-title">管理员登录</div>
        <div class="login-subtitle">
          账号/密码会在首次启动时生成并保存（SQLite），请在服务端控制台日志中查看。登录成功后会在浏览器保存 JWT（默认 30 天过期），刷新页面不会退出。
        </div>

        <div class="login-form">
          <div class="field">
            <label>账号</label>
            <input id="loginUser" autocomplete="username" placeholder="从服务端日志复制账号"/>
          </div>
          <div class="field">
            <label>密码（20 位）</label>
            <div class="input-with-action">
              <input id="loginPass" type="password" autocomplete="current-password" placeholder="从服务端日志复制密码"/>
              <button class="btn ghost sm input-action" id="togglePass" type="button">显示</button>
            </div>
          </div>

          <button class="btn primary login-btn" id="doLogin">登录</button>
          <div class="login-foot">提示：如果你修改了 <code>DATA_DIR</code>，请确认控制台打印的账号/密码对应同一个数据目录。</div>
        </div>
      </div>
    </div>
  `;

  async function doLogin() {
    try {
      const payload = {
        username: $("#loginUser").value.trim(),
        password: $("#loginPass").value
      };
      const res = await fetch("/api/login", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(payload)
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || data.ok === false) throw new Error(data.error || `HTTP ${res.status}`);
      if (!data.token) throw new Error("登录响应缺少 token");
      setToken(data.token);
      toast("登录成功");
      render();
    } catch (e) {
      toast(e.message, false);
    }
  }

  $("#doLogin").addEventListener("click", doLogin);
  $("#togglePass").addEventListener("click", () => {
    const input = $("#loginPass");
    const btn = $("#togglePass");
    const next = input.type === "password" ? "text" : "password";
    input.type = next;
    btn.textContent = next === "text" ? "隐藏" : "显示";
    input.focus();
  });
  $("#loginUser").focus();
  $("#loginPass").addEventListener("keydown", (e) => {
    if (e.key === "Enter") doLogin();
  });
}

async function renderSettings() {
  const [{ settings }, { status }] = await Promise.all([api("/api/settings"), api("/api/mihomo/status")]);
  const el = $("#view-settings");
  const installed = status.installed;
  const sys = status.system;
  const repo = status.repo;
  const proxyAuth = settings.proxyAuth || { enabled: false, username: "", password: "" };
  el.innerHTML = `
    ${pageHeader(
      "设置",
      "内核安装、端口分配与运行参数。",
      `<button class="btn primary" id="saveSettings">保存</button><button class="btn danger" id="logout">退出登录</button>`
    )}
    <div style="margin: 0 2px 10px 2px" class="badge">说明：本页设置会在「实例启动/重启」时写入实例配置；已在运行的实例需要停止后再启动才会生效。</div>

    <div class="grid">
      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">内核管理</div>
            <div class="panel-subtitle">内核会从 GitHub Release 下载并安装到本机（无需手动配置路径）。</div>
          </div>
          <div class="panel-actions">
            <button class="btn" id="checkMihomo">检查最新版本</button>
            <button class="btn ok" id="installMihomo">${installed ? "更新/修复安装" : "一键安装内核"}</button>
          </div>
        </div>

        <div class="badge">${installed ? `<span class="badge ok">内核已安装</span>` : `<span class="badge bad">内核未安装</span>`}</div>

        <div class="row" style="margin-top:10px">
          <div>
            <label>GitHub 仓库</label>
            <input id="mihomoRepo" value="${escapeHtml(repo)}" readonly />
            <div class="help">默认使用 <code>${escapeHtml(repo)}</code>；你也可以通过环境变量 <code>MIHOMO_REPO</code> 自定义。</div>
          </div>
          <div>
            <label>系统</label>
            <input id="mihomoSystem" value="${escapeHtml(sys.os)} / ${escapeHtml(sys.arch)}" readonly />
            <div class="help">会自动选择适配该系统的发布资源。</div>
          </div>
        </div>

        <div class="row">
          <div>
            <label>已安装版本</label>
            <input id="mihomoInstalled" value="${installed ? escapeHtml(installed.tag) : "-"}" readonly />
            <div class="help">${installed ? `资源：${escapeHtml(installed.assetName)}` : "尚未安装，无法启动实例。"}</div>
          </div>
          <div>
            <label>版本渠道</label>
            <select id="mihomoChannel">
              <option value="stable" selected>稳定版（推荐）</option>
              <option value="prerelease">含预发布</option>
            </select>
            <div class="help">「含预发布」可能更新更快，但稳定性不保证。</div>
          </div>
        </div>
      </div>

      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">基础网络</div>
            <div class="panel-subtitle">影响实例的监听与局域网访问（开启 LAN 访问前请先评估安全风险）。</div>
          </div>
        </div>

        <div class="row">
          <div>
            <label>监听地址</label>
            <input id="bindAddress" placeholder="127.0.0.1" value="${escapeHtml(settings.bindAddress || "127.0.0.1")}"/>
            <div class="help">写入实例配置 <code>bind-address</code>，决定每个实例的 <code>mixed-port</code> 监听在哪个地址。<code>127.0.0.1</code> 仅本机可用；如需局域网其它设备使用，可设为 <code>0.0.0.0</code> 或本机局域网 IP，并同时开启「允许局域网访问」。注意：这不会改变管理页端口（默认 <code>3320</code>）。</div>
          </div>
        </div>
        <div class="row">
          <div>
            <label>允许局域网访问</label>
            <select id="allowLan">
              <option value="false" ${settings.allowLan ? "" : "selected"}>否</option>
              <option value="true" ${settings.allowLan ? "selected" : ""}>是</option>
            </select>
            <div class="help">写入实例配置 <code>allow-lan</code>。开启后，配合「监听地址」可让局域网设备连接你的代理端口（每个实例一个端口）。注意安全风险：不要把代理端口暴露到公网，也不要在不可信网络中开启。</div>
          </div>
          <div>
            <label>日志级别</label>
            <select id="logLevel">
              ${[
                { v: "silent", t: "静默（silent）" },
                { v: "error", t: "错误（error）" },
                { v: "warning", t: "警告（warning）" },
                { v: "info", t: "信息（info）" },
                { v: "debug", t: "调试（debug）" }
              ]
                .map((o) => `<option value="${o.v}" ${settings.logLevel === o.v ? "selected" : ""}>${o.t}</option>`)
                .join("")}
            </select>
            <div class="help">写入实例配置 <code>log-level</code>。排障建议用「调试（debug）」，但会产生更多日志并略增 CPU/IO；日常使用建议「信息（info）/警告（warning）」。</div>
          </div>
        </div>
      </div>
    </div>

    <div class="panel">
      <div class="panel-header">
        <div>
          <div class="panel-title">代理认证</div>
          <div class="panel-subtitle">为每个实例的代理端口（mixed-port）启用用户名/密码认证（HTTP/SOCKS5）。</div>
        </div>
        <div class="panel-actions">
          <button class="btn" id="resetProxyAuth">重置凭据</button>
        </div>
      </div>

      <div class="row">
        <div>
          <label>启用认证</label>
          <select id="proxyAuthEnabled">
            <option value="false" ${proxyAuth.enabled ? "" : "selected"}>否</option>
            <option value="true" ${proxyAuth.enabled ? "selected" : ""}>是</option>
          </select>
          <div class="help">启用后，连接代理端口的客户端需要提供用户名和密码。</div>
        </div>
        <div>
          <label>用户名</label>
          <input id="proxyAuthUsername" value="${escapeHtml(proxyAuth.username || "")}" readonly />
          <div class="help">自动生成；如需更换请用「重置凭据」。</div>
        </div>
        <div>
          <label>密码</label>
          <div class="input-with-action">
            <input id="proxyAuthPassword" type="password" value="${escapeHtml(proxyAuth.password || "")}" readonly />
            <button class="btn ghost sm input-action" id="toggleProxyAuthPass" type="button">显示</button>
          </div>
          <div class="help">自动生成强密码；修改后需要重启实例生效。</div>
        </div>
      </div>

      <div class="help" style="margin-top:10px">
        提示：这里只影响「代理端口（mixed-port）」的认证，不影响管理后台登录。修改「启用认证」后请点击下方「保存」，并重启实例使配置生效。
      </div>
    </div>

    <div class="panel">
      <div class="panel-header">
        <div>
          <div class="panel-title">导出与复制</div>
          <div class="panel-subtitle">用于「复制实例链接」与「代理池」导出时的 host 选择。</div>
        </div>
        <div class="panel-actions">
          <button class="btn" id="detectPublicIp" type="button">自动获取公网 IP</button>
        </div>
      </div>

      <div class="row">
        <div>
          <label>导出 Host（公网 IP/域名）</label>
          <input id="exportHost" value="${escapeHtml(settings.exportHost || "")}" placeholder="例如：1.2.3.4 或 example.com" />
          <div class="help">
            生成「复制链接」与「代理池」列表时使用的 host。它只影响“显示/复制”的地址，不会改变实例实际监听地址（由「监听地址/允许局域网访问」控制）。建议：有域名就填域名；否则可点右上角自动获取公网 IP。若你只在局域网使用，也可以填局域网 IP。
          </div>
        </div>
      </div>

      <div class="help" style="margin-top:10px">
        提示：公网 IP 探测依赖外部 IP 服务；在 NAT/反代/安全组等场景，探测结果可能与外部可访问 IP 不一致，请以实际访问为准。
      </div>
    </div>

    <div class="panel">
      <div class="panel-header">
        <div>
          <div class="panel-title">端口与日志</div>
          <div class="panel-subtitle">用于自动分配每个实例的端口，并控制管理页日志缓存大小。</div>
        </div>
      </div>

      <div class="row">
        <div>
          <label>代理端口起始值</label>
          <input id="baseMixedPort" type="number" value="${settings.baseMixedPort}"/>
          <div class="help">创建实例时未手动指定端口，会从该值开始自动为实例分配 <code>mixed-port</code>（一个实例一个端口，对应一个出口节点）。若端口被系统占用或被其他实例占用，会自动跳过寻找下一个可用端口。</div>
        </div>
        <div>
          <label>控制端口起始值</label>
          <input id="baseControllerPort" type="number" value="${settings.baseControllerPort}"/>
          <div class="help">用于自动分配每个实例的 <code>external-controller</code> 端口（仅本机 <code>127.0.0.1</code> 访问）。它是 mihomo 的“控制接口”（HTTP API），管理器用它来做节点延迟检测、读取代理状态等；它不是代理端口，外部工具一般也不需要连接它。通常保持默认即可，只有在端口冲突时再调整。</div>
        </div>
      </div>

      <div class="row">
        <div>
          <label>实例日志保留行数</label>
          <input id="maxLogLines" type="number" value="${settings.maxLogLines}"/>
          <div class="help">管理页「日志」按钮读取的是管理器进程内存中缓存的“最近 N 行”。调大更利于排障，但会增加内存占用；该设置不影响 mihomo 自己的落盘日志策略（本项目默认不额外写文件日志）。</div>
        </div>
      </div>

      <div class="row">
        <div>
          <label>检测链接</label>
          <input id="healthCheckUrl" value="${escapeHtml(settings.healthCheckUrl || "")}" placeholder="http://www.gstatic.com/generate_204"/>
          <div class="help">用于「订阅节点」与「实例」的延迟检测（走 mihomo 的 <code>/delay</code> 接口）。参考 Clash Party 的默认值，推荐：<code>http://www.gstatic.com/generate_204</code>（HTTP 无 TLS 握手，通常更接近“纯网络延迟”）。如果你更希望检测“真实访问 HTTPS 站点”的体验，可改成 <code>https://www.gstatic.com/generate_204</code>；也可以用 <code>http://cp.cloudflare.com/generate_204</code>。</div>
        </div>
        <div style="flex:0.5;min-width:200px">
          <label>自动检测间隔（秒）</label>
          <input id="healthCheckIntervalSec" type="number" min="0" value="${settings.healthCheckIntervalSec ?? 60}"/>
          <div class="help">后台会按该间隔自动检测「运行中」实例的节点可用性（默认 <code>60</code> 秒）。同时，当实例开启「自动切换可用节点」时，该值会作为实例配置里 <code>fallback</code> 组的健康检查间隔。填 <code>0</code> 可关闭后台自动检测；但为避免自动切换配置失效，实例侧仍会使用 <code>60</code> 秒作为检查间隔。</div>
        </div>
      </div>
    </div>
  `;

  $("#saveSettings").addEventListener("click", async () => {
    try {
      const payload = {
        bindAddress: $("#bindAddress").value.trim() || "127.0.0.1",
        allowLan: $("#allowLan").value === "true",
        logLevel: $("#logLevel").value,
        exportHost: $("#exportHost").value.trim(),
        baseMixedPort: Number($("#baseMixedPort").value),
        baseControllerPort: Number($("#baseControllerPort").value),
        maxLogLines: Number($("#maxLogLines").value),
        healthCheckIntervalSec: Number($("#healthCheckIntervalSec").value),
        healthCheckUrl: $("#healthCheckUrl").value.trim(),
        proxyAuth: { enabled: $("#proxyAuthEnabled").value === "true" }
      };
      await api("/api/settings", { method: "PUT", body: JSON.stringify(payload) });
      toast("已保存");
    } catch (e) {
      toast(e.message, false);
    }
  });

  const channelValue = () => $("#mihomoChannel").value;
  const includePrerelease = () => channelValue() === "prerelease";

  $("#checkMihomo").addEventListener("click", async () => {
    const btn = $("#checkMihomo");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "检查中...";
    try {
      const { latest } = await api("/api/mihomo/latest", {
        method: "POST",
        body: JSON.stringify({ includePrerelease: includePrerelease() })
      });
      toast(`最新版本：${latest.tag}${latest.prerelease ? "（预发布）" : ""}`);
    } catch (e) {
      toast(e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });

  $("#installMihomo").addEventListener("click", async () => {
    const btn = $("#installMihomo");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "下载/安装中...";
    try {
      await api("/api/mihomo/install", {
        method: "POST",
        body: JSON.stringify({ includePrerelease: includePrerelease(), force: false })
      });
      toast("安装完成");
      renderSettings();
    } catch (e) {
      toast(e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });

  $("#logout").addEventListener("click", async () => {
    clearToken();
    toast("已退出");
    render();
  });

  $("#detectPublicIp")?.addEventListener?.("click", async () => {
    const input = $("#exportHost");
    const current = input.value.trim();
    if (current && !confirm("当前已设置导出 Host，是否用自动获取的公网 IP 覆盖？")) return;

    const btn = $("#detectPublicIp");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "获取中...";
    try {
      const { ip, exportHost } = await api("/api/settings/detect-public-ip", {
        method: "POST",
        body: JSON.stringify({ force: !!current })
      });
      input.value = exportHost || ip || "";
      toast(`已获取公网 IP：${ip}`);
    } catch (e) {
      toast(e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });

  $("#resetProxyAuth").addEventListener("click", async () => {
    if (!confirm("确定重置代理认证凭据？重置后旧凭据将失效。")) return;
    const btn = $("#resetProxyAuth");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "重置中...";
    try {
      const { proxyAuth } = await api("/api/settings/reset-proxy-auth", { method: "POST", body: "{}" });
      $("#proxyAuthUsername").value = proxyAuth?.username || "";
      $("#proxyAuthPassword").value = proxyAuth?.password || "";
      $("#proxyAuthEnabled").value = proxyAuth?.enabled ? "true" : "false";
      toast("已重置凭据");
    } catch (e) {
      toast(e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });

  $("#toggleProxyAuthPass").addEventListener("click", () => {
    const input = $("#proxyAuthPassword");
    const btn = $("#toggleProxyAuthPass");
    const next = input.type === "password" ? "text" : "password";
    input.type = next;
    btn.textContent = next === "text" ? "隐藏" : "显示";
    input.focus();
  });
}

async function renderSubscriptions() {
  const { subscriptions } = await api("/api/subscriptions");
  const el = $("#view-subscriptions");
  const totalProxies = subscriptions.reduce((sum, s) => sum + (s.proxies?.length || 0), 0);
  const okCount = subscriptions.filter((s) => !s.lastError).length;
  const errCount = subscriptions.length - okCount;
  el.innerHTML = `
    ${pageHeader("订阅", "导入机场订阅或粘贴 YAML，解析 proxies 列表。", `<button class="btn" id="refreshSubs">刷新</button>`)}

    <div class="grid">
      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">添加订阅</div>
            <div class="panel-subtitle">优先填写 URL；也可以直接粘贴 YAML（只解析 <code>proxies</code>）。</div>
          </div>
          <div class="panel-actions">
            <button class="btn primary" id="addSub">添加订阅</button>
          </div>
        </div>

        <div class="row">
          <div>
            <label>订阅名称</label>
            <input id="subName" placeholder="我的机场"/>
            <div class="help">用于在「实例」里快速定位订阅来源。</div>
          </div>
          <div>
            <label>订阅 URL（可选）</label>
            <input id="subUrl" placeholder="https://..."/>
            <div class="help">填写后可在列表里一键刷新订阅。</div>
          </div>
        </div>
        <div>
          <label>或直接粘贴 YAML（可选）</label>
          <textarea id="subYaml" placeholder="proxies:\n  - name: ..."></textarea>
          <div class="help">适合临时导入或 URL 不方便提供的场景。</div>
        </div>
      </div>

      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">概览</div>
            <div class="panel-subtitle">订阅与节点统计（仅用于管理页展示）。</div>
          </div>
        </div>

        <div class="stats">
          <div class="stat">
            <div class="stat-value">${subscriptions.length}</div>
            <div class="stat-label">订阅数量</div>
          </div>
          <div class="stat">
            <div class="stat-value">${totalProxies}</div>
            <div class="stat-label">节点总数（proxies）</div>
          </div>
          <div class="stat">
            <div class="stat-value" style="color: var(--ok)">${okCount}</div>
            <div class="stat-label">正常订阅</div>
          </div>
          <div class="stat">
            <div class="stat-value" style="color: var(--danger)">${errCount}</div>
            <div class="stat-label">错误订阅</div>
          </div>
        </div>
      </div>
    </div>

    <div class="panel">
      <div class="panel-header">
        <div>
          <div class="panel-title">订阅列表</div>
          <div class="panel-subtitle">URL 订阅支持一键刷新；粘贴 YAML 的订阅只保存快照。</div>
        </div>
      </div>

      <div class="table-wrap">
        <table class="table">
          <thead>
            <tr><th>名称</th><th>URL</th><th>节点数</th><th>更新时间</th><th>状态</th><th>操作</th></tr>
          </thead>
          <tbody>
            ${
              subscriptions.length
                ? subscriptions
                    .map((s) => {
                      const name = escapeHtml(s.name);
                      const url = escapeHtml(s.url || "-");
                      const err = s.lastError ? escapeHtml(s.lastError) : "";
                      return `
                        <tr>
                          <td>${name}</td>
                          <td style="max-width:360px;word-break:break-all;color:var(--muted)">${url}</td>
	                          <td>${s.proxies.length}</td>
	                          <td>${fmtDate(s.updatedAt)}</td>
	                          <td>${s.lastError ? `<span class="badge bad">错误</span> ${err}` : `<span class="badge ok">正常</span>`}</td>
	                          <td>
	                            <div class="btn-group">
	                              <button class="btn" data-proxies="${escapeHtml(s.id)}">节点</button>
	                              ${s.url ? `<button class="btn" data-refresh="${escapeHtml(s.id)}">刷新</button>` : ""}
	                              <button class="btn danger" data-sub-del="${escapeHtml(s.id)}">删除</button>
	                            </div>
	                          </td>
	                        </tr>
	                      `;
	                    })
	                    .join("")
                : `<tr><td colspan="6" class="muted">暂无订阅，请先添加一个订阅或粘贴 YAML。</td></tr>`
            }
          </tbody>
        </table>
      </div>
    </div>
  `;

  $("#refreshSubs").addEventListener("click", () => render());

  $("#addSub").addEventListener("click", async () => {
    const btn = $("#addSub");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "添加中...";
    try {
      const name = $("#subName").value.trim();
      const url = $("#subUrl").value.trim();
      const yaml = $("#subYaml").value;
      if (!name) throw new Error("请填写订阅名称");
      if (!url && !yaml.trim()) throw new Error("请填写订阅 URL 或粘贴 YAML");

      const payload = {
        name,
        url,
        yaml
      };
      await api("/api/subscriptions", { method: "POST", body: JSON.stringify(payload) });
      toast("已添加");
      render();
    } catch (e) {
      toast(e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });

  $$("[data-refresh]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      const id = btn.dataset.refresh;
      try {
        await api(`/api/subscriptions/${id}/refresh`, { method: "POST", body: "{}" });
        toast("已刷新");
        render();
      } catch (e) {
        toast(e.message, false);
      }
    })
  );

  $$("[data-sub-del]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      const id = btn.dataset.subDel;
      const sub = subscriptions.find((s) => s.id === id);
      const name = sub ? sub.name : id;
      if (!confirm(`确定删除订阅「${name}」？\n\n注意：如果该订阅仍有实例在使用，会拒绝删除。`)) return;

      const old = btn.textContent;
      btn.disabled = true;
      btn.textContent = "删除中...";
      try {
        await api(`/api/subscriptions/${id}`, { method: "DELETE" });
        toast("已删除");
        render();
      } catch (e) {
        toast(e.message, false);
      } finally {
        btn.disabled = false;
        btn.textContent = old;
      }
    })
  );

	  $$("[data-proxies]").forEach((btn) =>
	    btn.addEventListener("click", async () => {
	      const id = btn.dataset.proxies;
	      const sub = subscriptions.find((s) => s.id === id);
	      const title = sub ? `节点列表 · ${sub.name}` : "节点列表";
	      const old = btn.textContent;
	      btn.disabled = true;
	      btn.textContent = "加载中...";
	      try {
	        const { proxies } = await api(`/api/subscriptions/${id}/proxies`);
	        const items = (proxies || []).map((p, idx) => {
	          const rawName = typeof p?.name === "string" ? p.name : `#${idx + 1}`;
	          const name = escapeHtml(rawName);
	          const type = escapeHtml(p?.type || "-");
	          const server = typeof p?.server === "string" ? p.server : "";
	          const port = typeof p?.port === "number" || typeof p?.port === "string" ? String(p.port) : "";
	          const addr = server ? `${escapeHtml(server)}${port ? `:${escapeHtml(port)}` : ""}` : "-";
	          const network = typeof p?.network === "string" ? escapeHtml(p.network) : "-";
	          const tls = typeof p?.tls === "boolean" ? (p.tls ? "是" : "否") : "-";
	          const udp = typeof p?.udp === "boolean" ? (p.udp ? "是" : "否") : "-";
	          const health = p?.health || null;
	          return { rawName, name, type, addr, network, tls, udp, health };
	        });

	        function renderRows(filterText) {
	          const q = String(filterText || "").trim().toLowerCase();
	          const filtered = q ? items.filter((it) => it.rawName.toLowerCase().includes(q)) : items;
	          const countEl = $("#proxyCount");
	          if (countEl) countEl.textContent = `共 ${filtered.length} 个`;
	          const bodyEl = $("#proxyRows");
	          if (!bodyEl) return;
	          const healthCell = (h) => {
	            if (!h?.checkedAt) return `<span class="badge">未检测</span>`;
	            const at = fmtDate(h.checkedAt);
	            const ms = typeof h.latencyMs === "number" ? `${Math.round(h.latencyMs)}ms` : "-";
	            if (h.ok) {
	              return `<span class="badge ok">可用</span> <span class="muted">${ms}</span><div class="muted" style="font-size:12px;margin-top:4px">${escapeHtml(at)}</div>`;
	            }
	            const err = escapeHtml(h.error || "不可用");
	            return `<span class="badge bad">不可用</span> <span class="muted">${ms}</span><div class="muted" style="font-size:12px;margin-top:4px;max-width:360px;word-break:break-all">${err}</div><div class="muted" style="font-size:12px;margin-top:4px">${escapeHtml(at)}</div>`;
	          };
	          bodyEl.innerHTML =
	            filtered.length > 0
	              ? filtered
	                  .map(
	                    (it) => `
	                      <tr>
	                        <td style="max-width:340px;word-break:break-all">${it.name}</td>
	                        <td>${it.type}</td>
	                        <td style="max-width:320px;word-break:break-all;color:var(--muted)">${it.addr}</td>
	                        <td>${it.network}</td>
	                        <td>${it.tls}</td>
	                        <td>${it.udp}</td>
	                        <td style="min-width:220px">${healthCell(it.health)}</td>
	                        <td style="white-space:nowrap"><button class="btn sm" data-proxy-check="${escapeHtml(it.rawName)}">检测</button></td>
	                      </tr>
	                    `
	                  )
	                  .join("")
	              : `<tr><td colspan="8" class="muted">没有匹配的节点。</td></tr>`;
	        }

	        openModal({
	          title,
	          actionsHtml: `<button class="btn sm" id="checkAllProxies" type="button">检测全部</button><button class="btn sm" id="copyProxyNames" type="button">复制节点名称</button>`,
	          bodyHtml: `
	            <div class="help" style="margin-bottom:10px">说明：支持对单个节点/全部节点进行延迟检测（检测链接在「设置」里可配置）。敏感字段（如密码/UUID）不会在此页展示。</div>
	            <div class="row" style="margin-bottom:10px">
	              <div>
	                <input id="proxyFilter" placeholder="搜索节点名称（支持模糊匹配）" />
	              </div>
	              <div style="flex:0.5;min-width:140px">
	                <div class="badge" id="proxyCount">共 ${items.length} 个</div>
	              </div>
	            </div>
	            <div class="table-wrap">
	              <table class="table">
	                <thead>
	                  <tr><th>名称</th><th>类型</th><th>地址</th><th>网络</th><th>TLS</th><th>UDP</th><th>可用性</th><th>操作</th></tr>
	                </thead>
	                <tbody id="proxyRows">
	                  <tr><td colspan="8" class="muted">${items.length ? "加载中..." : "该订阅暂无节点（proxies 为空）。"}</td></tr>
	                </tbody>
	              </table>
	            </div>
	          `
	        });

	        // 填充 rows（用 JS 渲染，便于搜索）
	        renderRows("");
	        const inputEl = $("#proxyFilter");
	        inputEl?.addEventListener?.("input", () => renderRows(inputEl.value));

	        async function doCheck(names, all = false) {
	          const payload = all ? { all: true } : names.length === 1 ? { proxyName: names[0] } : { names };
	          const { results } = await api(`/api/subscriptions/${id}/proxies/check`, {
	            method: "POST",
	            body: JSON.stringify(payload)
	          });
	          for (const it of items) {
	            const r = results?.[it.rawName];
	            if (r) it.health = r;
	          }
	          renderRows(inputEl?.value || "");
	          return results;
	        }

	        $("#checkAllProxies")?.addEventListener?.("click", async () => {
	          const btn2 = $("#checkAllProxies");
	          const old2 = btn2.textContent;
	          btn2.disabled = true;
	          btn2.textContent = "检测中...";
	          try {
	            await doCheck([], true);
	            toast("检测完成");
	          } catch (e) {
	            toast(e.message, false);
	          } finally {
	            btn2.disabled = false;
	            btn2.textContent = old2;
	          }
	        });

	        $("#modalBody").onclick = async (ev) => {
	          const target = ev.target;
	          const b = target && target.closest ? target.closest("[data-proxy-check]") : null;
	          if (!b) return;
	          const name = b.dataset.proxyCheck;
	          const oldTxt = b.textContent;
	          b.disabled = true;
	          b.textContent = "检测中...";
	          try {
	            const results = await doCheck([name], false);
	            const r = results?.[name];
	            if (r?.ok) {
	              const ms = typeof r.latencyMs === "number" ? `${Math.round(r.latencyMs)}ms` : "-";
	              toast(`可用 · ${ms}`);
	            } else {
	              toast(`不可用：${r?.error || "检测失败"}`, false);
	            }
	          } catch (e) {
	            toast(e.message, false);
	          } finally {
	            b.disabled = false;
	            b.textContent = oldTxt;
	          }
	        };

	        $("#copyProxyNames")?.addEventListener?.("click", async () => {
	          try {
	            const names = (proxies || []).map((p) => p?.name).filter(Boolean).join("\n");
	            await navigator.clipboard.writeText(names);
            toast("已复制");
          } catch {
            toast("复制失败（浏览器权限限制）", false);
          }
        });
      } catch (e) {
        toast(e.message, false);
      } finally {
        btn.disabled = false;
        btn.textContent = old;
      }
    })
  );
}

async function renderInstances() {
  const [{ instances }, { subscriptions }, { settings }] = await Promise.all([api("/api/instances"), api("/api/subscriptions"), api("/api/settings")]);
  const el = $("#view-instances");

  const ALL_SUB = "__ALL__";
  const AUTO_PROXY = "__AUTO__";

  const runningCount = instances.filter((i) => i.runtime?.running).length;
  const totalProxies = subscriptions.reduce((sum, s) => sum + (s.proxies?.length || 0), 0);

  const subOptions = subscriptions.length
    ? [
        `<option value="${ALL_SUB}">全部订阅（${subscriptions.length} 个 / ${totalProxies} 节点）</option>`,
        ...subscriptions.map((s) => `<option value="${escapeHtml(s.id)}">${escapeHtml(s.name)}（${s.proxies.length}）</option>`)
      ].join("")
    : `<option value="">（暂无订阅，请先去「订阅」添加）</option>`;

  el.innerHTML = `
    ${pageHeader(
      "实例",
      "每个实例对应一个端口（mixed-port）；可开启自动切换以保持端口高可用。",
      `<button class="btn" id="refreshInst">刷新</button><button class="btn" id="checkAllInst">检测全部</button>`
    )}

    <div class="grid">
      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">创建实例</div>
            <div class="panel-subtitle">创建/启动前会先检测节点可用性；批量创建会从「可用且未被占用」的节点里按延迟优先分配。</div>
          </div>
          <div class="panel-actions">
            <button class="btn primary" id="createInst">创建实例</button>
            <button class="btn primary" id="createBatch">批量创建</button>
          </div>
        </div>

        <div class="row">
          <div>
            <label>选择订阅</label>
            <select id="instSub" ${subscriptions.length ? "" : "disabled"}>${subOptions}</select>
            <div class="help">选择「全部订阅」可把所有订阅的节点当作一个池来使用（创建实例时会自动避开已占用节点）。</div>
          </div>
          <div>
            <label>选择节点</label>
            <select id="instProxy" ${subscriptions.length ? "" : "disabled"}><option value="${AUTO_PROXY}">全部节点（自动选择未占用）</option></select>
            <div class="help">每个实例绑定一个节点（同一节点只能被一个实例占用）。选择「全部节点」会自动从未占用节点里挑选。</div>
          </div>
        </div>

        <div class="row">
          <div>
            <label>mixed-port（可选）</label>
            <input id="instPort" type="number" placeholder="留空自动分配" ${subscriptions.length ? "" : "disabled"}/>
            <div class="help">不填则从「设置」里的「代理端口起始值」开始自动分配。</div>
          </div>
          <div>
            <label>创建后自动启动</label>
            <select id="instAuto" ${subscriptions.length ? "" : "disabled"}>
              <option value="true" selected>是（推荐）</option>
              <option value="false">否</option>
            </select>
            <div class="help">开启后会在创建完成后立即拉起进程。</div>
          </div>
          <div>
            <label>自动切换可用节点</label>
            <select id="instAutoSwitch" ${subscriptions.length ? "" : "disabled"}>
              <option value="true" selected>开（推荐）</option>
              <option value="false">关（固定使用选定节点）</option>
            </select>
            <div class="help">开启后，实例会优先使用选定节点；当节点不可用时自动切换到同订阅内其它可用节点以保持端口高可用（出口 IP 可能变化）。</div>
          </div>
          <div>
            <label>批量数量</label>
            <input id="batchCount" type="number" min="1" value="5" ${subscriptions.length ? "" : "disabled"}/>
            <div class="help">批量创建会自动选择「可用节点」（需先在订阅节点里进行检测）。</div>
          </div>
        </div>

        <div class="row" style="margin-top:6px">
          <div class="badge" id="createHint">提示：创建/启动实例前会先做节点延迟检测；检测结果会保存到 SQLite（全局）。</div>
          <div class="badge" id="availInfo">剩余可用节点：-</div>
        </div>
      </div>

      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">概览</div>
            <div class="panel-subtitle">实例运行状态与节点统计。</div>
          </div>
        </div>

        <div class="stats">
          <div class="stat">
            <div class="stat-value">${instances.length}</div>
            <div class="stat-label">实例数量</div>
          </div>
          <div class="stat">
            <div class="stat-value" style="color: var(--ok)">${runningCount}</div>
            <div class="stat-label">运行中实例</div>
          </div>
          <div class="stat">
            <div class="stat-value">${subscriptions.length}</div>
            <div class="stat-label">订阅数量</div>
          </div>
          <div class="stat">
            <div class="stat-value">${totalProxies}</div>
            <div class="stat-label">节点总数（proxies）</div>
          </div>
        </div>

        <div class="help" style="margin-top:10px">
          需要批量导出 <code>host:port</code> 列表时，去「代理池」页点击复制即可。
        </div>
      </div>
    </div>

    <div class="panel">
      <div class="panel-header">
        <div>
          <div class="panel-title">实例列表</div>
          <div class="panel-subtitle">启动/停止会在后台拉起或结束对应的 mihomo 进程。</div>
        </div>
      </div>

      <div class="table-wrap">
        <table class="table">
          <thead>
            <tr><th>名称</th><th>端口</th><th>PID</th><th>运行状态</th><th>可用性</th><th>创建时间</th><th>操作</th></tr>
          </thead>
          <tbody>
            ${
              instances.length
                ? instances
                    .map((i) => {
                      const running = i.runtime?.running;
                      const name = escapeHtml(i.name);
                      const rawProxyName = String(i.proxyName || "");
                      const proxyName = escapeHtml(rawProxyName);
                      const autoSwitch = !!i.autoSwitch;
                      const pid = i.runtime?.pid ?? "-";
                      const health = i.health;
                      const activeProxyName =
                        typeof health?.proxyName === "string" && health.proxyName.trim() ? health.proxyName.trim() : "";
                      const activeLine =
                        activeProxyName && activeProxyName !== rawProxyName
                          ? `<div style="color:var(--muted);font-size:12px;margin-top:4px">当前=${escapeHtml(activeProxyName)}</div>`
                          : "";
                      const autoSwitchBadge = autoSwitch
                        ? `<span class="badge ok">自动切换：开</span>`
                        : `<span class="badge">自动切换：关</span>`;
                      const isChecking = checkingInstances.has(i.id);
                      let healthHtml = `<span class="badge">未检测</span>`;
                      if (isChecking) {
                        healthHtml = `<span class="badge" style="background:rgba(100,149,237,0.15);color:#6495ed">检测中...</span>`;
                      } else if (health?.checkedAt) {
                        const at = fmtDate(health.checkedAt);
                        const ms = typeof health.latencyMs === "number" ? `${Math.round(health.latencyMs)}ms` : "-";
                        if (health.ok) {
                          healthHtml = `<span class="badge ok">可用</span> <span class="muted">${ms}</span><div class="muted" style="font-size:12px;margin-top:4px">${at}</div>`;
                        } else {
                          const err = escapeHtml(health.error || "不可用");
                          healthHtml = `<span class="badge bad">不可用</span> <span class="muted">${ms}</span><div class="muted" style="font-size:12px;margin-top:4px;max-width:320px;word-break:break-all">${err}</div><div class="muted" style="font-size:12px;margin-top:4px">${at}</div>`;
                        }
                      }
                      return `
                        <tr>
                          <td style="max-width:420px;word-break:break-all">
                            ${name}
                            <div style="color:var(--muted);font-size:12px;margin-top:4px">proxy=${proxyName}</div>
                            ${activeLine}
                            <div style="margin-top:8px">${autoSwitchBadge}</div>
                          </td>
                          <td>${i.mixedPort}</td>
                          <td>${pid}</td>
	                          <td>${running ? `<span class="badge ok">运行中</span>` : `<span class="badge bad">已停止</span>`}</td>
	                          <td>${healthHtml}</td>
	                          <td>${fmtDate(i.createdAt)}</td>
	                          <td>
	                            <div class="btn-group">
	                              ${
	                                running
	                                  ? `<button class="btn danger" data-stop="${escapeHtml(i.id)}">停止</button>`
	                                  : `<button class="btn ok" data-start="${escapeHtml(i.id)}">启动</button>`
	                              }
	                              <button class="btn" data-check="${escapeHtml(i.id)}">检测</button>
	                              <button class="btn" data-copy="${escapeHtml(i.id)}">复制链接</button>
	                              <button class="btn" data-edit="${escapeHtml(i.id)}">编辑</button>
	                              <button class="btn" data-logs="${escapeHtml(i.id)}">日志</button>
	                              <button class="btn danger" data-del="${escapeHtml(i.id)}">删除</button>
	                            </div>
	                          </td>
	                        </tr>
	                      `;
	                    })
	                    .join("")
                : `<tr><td colspan="7" class="muted">暂无实例，请先创建一个实例。</td></tr>`
            }
          </tbody>
        </table>
      </div>
    </div>
  `;

  $("#refreshInst").addEventListener("click", () => render());
  $("#checkAllInst").addEventListener("click", async () => {
    const btn = $("#checkAllInst");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "检测中...";
    try {
      await api("/api/instances/check-all", { method: "POST", body: "{}" });
      toast("检测完成");
      render();
    } catch (e) {
      toast(e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });

  const createBtn = $("#createInst");
  const batchBtn = $("#createBatch");
  const createHint = $("#createHint");
  const availInfo = $("#availInfo");

  function setCreateEnabled(enabled, hint) {
    createBtn.disabled = !enabled;
    if (hint) createHint.textContent = hint;
  }

  async function refreshAvailability() {
    const subId = $("#instSub").value;
    if (!subId) {
      if (availInfo) availInfo.textContent = "剩余可用节点：-";
      return;
    }
    try {
      const path = subId === ALL_SUB ? "/api/subscriptions/availability" : `/api/subscriptions/${subId}/availability`;
      const { availability } = await api(path);
      if (availInfo) availInfo.textContent = `剩余可用节点：${availability.available}（总${availability.total} / 已用${availability.used} / 未测${availability.untested} / 不可用${availability.unhealthy}）`;
    } catch {
      if (availInfo) availInfo.textContent = "剩余可用节点：-（获取失败）";
    }
  }

  async function refreshProxyOptions() {
    const subId = $("#instSub").value;
    const sel = $("#instProxy");

    if (!subscriptions.length) {
      sel.innerHTML = `<option value="">（暂无节点）</option>`;
      batchBtn.disabled = true;
      setCreateEnabled(false, "提示：暂无订阅，请先去「订阅」添加。");
      await refreshAvailability();
      return;
    }

    const usedKey = (subscriptionId, proxyName) => `${subscriptionId}::${proxyName}`;
    const used = new Set(instances.map((i) => usedKey(i.subscriptionId, i.proxyName)));

    const isAll = subId === ALL_SUB;
    const items = [];
    if (isAll) {
      for (const s of subscriptions) {
        for (const p of s.proxies || []) {
          if (!p?.name) continue;
          items.push({ subscriptionId: s.id, subscriptionName: s.name, proxyName: p.name });
        }
      }
    } else {
      const sub = subscriptions.find((s) => s.id === subId);
      if (!sub) {
        sel.innerHTML = `<option value="">（无）</option>`;
        batchBtn.disabled = true;
        setCreateEnabled(false, "提示：请选择订阅后再创建实例。");
        await refreshAvailability();
        return;
      }
      for (const p of sub.proxies || []) {
        if (!p?.name) continue;
        items.push({ subscriptionId: sub.id, subscriptionName: sub.name, proxyName: p.name });
      }
    }

    if (!items.length) {
      sel.innerHTML = `<option value="">（该订阅没有 proxies）</option>`;
      batchBtn.disabled = true;
      setCreateEnabled(false, "提示：该订阅没有 proxies，请检查订阅内容。");
      await refreshAvailability();
      return;
    }

    const isUsed = (it) => used.has(usedKey(it.subscriptionId, it.proxyName));
    const unusedCount = items.filter((it) => !isUsed(it)).length;

    items.sort((a, b) => {
      const ua = isUsed(a) ? 1 : 0;
      const ub = isUsed(b) ? 1 : 0;
      if (ua !== ub) return ua - ub;
      const sa = a.subscriptionName.localeCompare(b.subscriptionName, "zh-CN");
      if (sa !== 0) return sa;
      return a.proxyName.localeCompare(b.proxyName, "zh-CN");
    });

    const optionsHtml = [
      `<option value="${AUTO_PROXY}">全部节点（自动选择未占用）</option>`,
      ...items.map((it) => {
        const usedNow = isUsed(it);
        const label = isAll ? `${it.subscriptionName} · ${it.proxyName}` : it.proxyName;
        const text = usedNow ? `${label}（已占用）` : label;
        const value = isAll
          ? escapeHtml(JSON.stringify({ subscriptionId: it.subscriptionId, proxyName: it.proxyName }))
          : escapeHtml(it.proxyName);
        return `<option value="${value}" ${usedNow ? "disabled" : ""}>${escapeHtml(text)}</option>`;
      })
    ].join("");

    sel.innerHTML = optionsHtml;

    batchBtn.disabled = unusedCount <= 0;
    if (unusedCount <= 0) {
      setCreateEnabled(false, "提示：没有未被占用的节点可用于创建实例（请删除旧实例释放节点）。");
    } else {
      setCreateEnabled(
        true,
        isAll
          ? "提示：已选择“全部订阅”，可用「全部节点」让系统自动分配未占用节点。"
          : "提示：你也可以选择「全部节点」让系统自动分配未占用节点。"
      );
    }

    await refreshAvailability();
  }

  $("#instSub").addEventListener("change", refreshProxyOptions);
  await refreshProxyOptions();

  $("#createInst").addEventListener("click", async () => {
    const btn = $("#createInst");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "创建中...";
    try {
      const subSel = $("#instSub").value;
      const proxySel = $("#instProxy").value;
      if (!subSel) throw new Error("请先选择订阅");
      if (!proxySel) throw new Error("请先选择节点");

      let subscriptionId = subSel;
      let proxyName = proxySel;

      if (subSel === ALL_SUB) {
        if (proxySel === AUTO_PROXY) {
          subscriptionId = "";
          proxyName = AUTO_PROXY;
        } else {
          let parsed = null;
          try {
            parsed = JSON.parse(proxySel);
          } catch {
            parsed = null;
          }
          if (!parsed || typeof parsed.subscriptionId !== "string" || typeof parsed.proxyName !== "string") {
            throw new Error("请选择节点");
          }
          subscriptionId = parsed.subscriptionId;
          proxyName = parsed.proxyName;
        }
      } else if (proxySel === AUTO_PROXY) {
        proxyName = AUTO_PROXY;
      }

      const payload = {
        subscriptionId,
        proxyName,
        mixedPort: $("#instPort").value ? Number($("#instPort").value) : undefined,
        autoStart: $("#instAuto").value === "true",
        autoSwitch: $("#instAutoSwitch").value === "true"
      };
      const { instance } = await api("/api/instances", { method: "POST", body: JSON.stringify(payload) });
      toast("已创建");
      render();
      // 如果自动启动了，后台触发检测（不阻塞）
      if (payload.autoStart && instance?.id) {
        checkInstanceInBackground(instance.id);
      }
    } catch (e) {
      toast(e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });

  $("#createBatch").addEventListener("click", async () => {
    const btn = $("#createBatch");
    const old = btn.textContent;
    btn.disabled = true;
    btn.textContent = "创建中...";
    try {
      const subscriptionId = $("#instSub").value;
      if (!subscriptionId) throw new Error("请先选择订阅");
      const count = Number($("#batchCount").value);
      if (!Number.isInteger(count) || count < 1) throw new Error("批量数量必须为正整数");

      const payload = {
        subscriptionId,
        count,
        autoStart: $("#instAuto").value === "true",
        autoSwitch: $("#instAutoSwitch").value === "true"
      };
      const { created, startErrors } = await api("/api/instances/batch", { method: "POST", body: JSON.stringify(payload) });
      const errCount = startErrors ? Object.keys(startErrors).length : 0;
      if (errCount) toast(`已创建，但有 ${errCount} 个实例启动失败（可到实例列表查看/重试）`, false);
      else toast("批量创建成功");
      render();
      // 如果自动启动了，对成功启动的实例后台触发检测
      if (payload.autoStart && created?.length) {
        const failedIds = new Set(Object.keys(startErrors || {}));
        for (const inst of created) {
          if (inst?.id && !failedIds.has(inst.id)) {
            checkInstanceInBackground(inst.id);
          }
        }
      }
    } catch (e) {
      toast(e.message, false);
    } finally {
      btn.disabled = false;
      btn.textContent = old;
    }
  });

  $$("[data-start]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      const instanceId = btn.dataset.start;
      const old = btn.textContent;
      btn.disabled = true;
      btn.textContent = "启动中...";
      try {
        await api(`/api/instances/${instanceId}/start`, { method: "POST", body: "{}" });
        toast("已启动");
        render();
        // 启动成功后后台触发检测
        checkInstanceInBackground(instanceId);
      } catch (e) {
        toast(e.message, false);
      } finally {
        btn.disabled = false;
        btn.textContent = old;
      }
    })
  );

  $$("[data-stop]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      const old = btn.textContent;
      btn.disabled = true;
      btn.textContent = "停止中...";
      try {
        await api(`/api/instances/${btn.dataset.stop}/stop`, { method: "POST", body: "{}" });
        toast("已停止");
        render();
      } catch (e) {
        toast(e.message, false);
      } finally {
        btn.disabled = false;
        btn.textContent = old;
      }
    })
  );

  $$("[data-del]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      if (!confirm("确定删除该实例？（会先停止进程）")) return;
      const old = btn.textContent;
      btn.disabled = true;
      btn.textContent = "删除中...";
      try {
        await api(`/api/instances/${btn.dataset.del}`, { method: "DELETE" });
        toast("已删除");
        render();
      } catch (e) {
        toast(e.message, false);
      } finally {
        btn.disabled = false;
        btn.textContent = old;
      }
    })
  );

  $$("[data-check]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      const id = btn.dataset.check;
      const old = btn.textContent;
      btn.disabled = true;
      btn.textContent = "检测中...";
      try {
        const { health } = await api(`/api/instances/${id}/check`, { method: "POST", body: "{}" });
        if (health?.ok) {
          const ms = typeof health.latencyMs === "number" ? `${Math.round(health.latencyMs)}ms` : "-";
          toast(`节点可用 · ${ms}`);
        } else {
          toast(`不可用：${health?.error || "检测失败"}`, false);
        }
        render();
      } catch (e) {
        toast(e.message, false);
      } finally {
        btn.disabled = false;
        btn.textContent = old;
      }
    })
  );

  $$("[data-edit]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      const id = btn.dataset.edit;
      const inst = instances.find((i) => i.id === id);
      if (!inst) return toast("实例不存在", false);
      const running = !!inst.runtime?.running;

      openFormModal({
        title: `编辑 · ${inst.name}`,
        bodyHtml: `
          <div class="field">
            <label>自动切换可用节点</label>
            <select id="editAutoSwitch">
              <option value="true" ${inst.autoSwitch ? "selected" : ""}>开（推荐）</option>
              <option value="false" ${inst.autoSwitch ? "" : "selected"}>关（固定节点）</option>
            </select>
            <div class="help">
              开启：实例会把同订阅的节点写入 <code>fallback</code> 组；当当前节点不可用时自动切换以保持端口高可用（出口 IP 可能变化）。<br/>
              关闭：实例只使用单个节点（与创建时选择一致）。<br/>
              ${running ? "提示：该实例正在运行，保存后会自动重启以生效。" : "提示：该实例未运行，保存后下次启动生效。"}
            </div>
          </div>
        `,
        onSubmit: async () => {
          const autoSwitch = $("#editAutoSwitch").value === "true";
          await api(`/api/instances/${id}`, { method: "PUT", body: JSON.stringify({ autoSwitch }) });
          toast("已保存");
          closeModal();
          render();
        }
      });
    })
  );

  $$("[data-copy]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      const id = btn.dataset.copy;
      const inst = instances.find((i) => i.id === id);
      if (!inst) return toast("实例不存在", false);

      const proxyAuth = settings?.proxyAuth || { enabled: false, username: "", password: "" };
      const allowLan = !!settings?.allowLan;
      const bindAddress = String(settings?.bindAddress || "127.0.0.1").trim() || "127.0.0.1";
      const exportHost = String(settings?.exportHost || "").trim();

      // 选择一个对用户可用的 host：
      // - 优先使用「设置」里的导出 Host（公网 IP/域名/局域网 IP 都可）
      // - 若未设置，则回退：allowLan=false -> 127.0.0.1；allowLan=true -> bindAddress/本机 IP/当前 hostname
      let host = exportHost;
      let hostHint = "";
      if (host) {
        hostHint = `已使用导出 Host：${host}（可在「设置」里修改）`;
      } else {
        host = "127.0.0.1";
        if (allowLan) {
          if (bindAddress && bindAddress !== "0.0.0.0" && bindAddress !== "127.0.0.1") host = bindAddress;
          else if (location.hostname) host = location.hostname;
        }
        hostHint = `未设置导出 Host，已回退使用：${host}（建议在「设置」里填写或点“自动获取公网 IP”）`;
      }

      const wrapHost = (h) => {
        const s = String(h || "").trim();
        if (!s) return "127.0.0.1";
        // IPv6 URL 需要加 []
        if (s.includes(":") && !s.startsWith("[")) return `[${s}]`;
        return s;
      };

      const userinfo = proxyAuth.enabled
        ? `${encodeURIComponent(String(proxyAuth.username || ""))}:${encodeURIComponent(String(proxyAuth.password || ""))}@`
        : "";

      const hostPart = wrapHost(host);
      const port = Number(inst.mixedPort);

      const socks5Url = `socks5://${userinfo}${hostPart}:${port}`;
      const httpUrl = `http://${userinfo}${hostPart}:${port}`;

      const authHint = proxyAuth.enabled
        ? "已启用认证（URL 已自动编码用户名/密码）"
        : "未启用认证：链接不包含账号密码（可在「设置 → 代理认证」启用）";
      const lanHint = allowLan
        ? "已开启「允许局域网访问」：局域网设备可直接使用链接；如需公网访问，请确保防火墙/端口映射/安全组已放行实例端口。"
        : "未开启「允许局域网访问」：代理端口通常仅本机可用；如需其他设备/公网访问，请在设置开启 allow-lan，并将 bind-address 设为 0.0.0.0 或本机局域网 IP，再放行端口。";

      // 一键复制（默认 SOCKS5）
      const copied = await copyToClipboard(socks5Url);
      if (copied) toast("已复制 SOCKS5 链接");
      else toast("复制失败（浏览器权限限制）", false);

      openModal({
        title: `复制链接 · ${inst.name}`,
        actionsHtml: `<button class="btn sm" id="copySocks5Btn" type="button">复制 SOCKS5</button><button class="btn sm" id="copyHttpBtn" type="button">复制 HTTP</button>`,
        bodyHtml: `
          <div class="row">
            <div>
              <label>SOCKS5</label>
              <input id="socks5Url" readonly value="${escapeHtml(socks5Url)}"/>
            </div>
            <div>
              <label>HTTP</label>
              <input id="httpUrl" readonly value="${escapeHtml(httpUrl)}"/>
            </div>
          </div>
          <div class="help" style="margin-top:10px">
            ${escapeHtml(authHint)}<br/>
            ${escapeHtml(hostHint)}<br/>
            ${escapeHtml(lanHint)}<br/>
            实例端口：<code>${escapeHtml(String(port))}</code>；host：<code>${escapeHtml(String(host))}</code><br/>
            说明：本项目的 <code>mixed-port</code> 同时支持 HTTP 与 SOCKS5，你可以按目标应用的要求选择其一。
          </div>
        `
      });

      const doCopy = async (text, label) => {
        const ok = await copyToClipboard(text);
        if (ok) toast(`已复制 ${label}`);
        else toast("复制失败（浏览器权限限制）", false);
      };

      $("#copySocks5Btn")?.addEventListener?.("click", () => doCopy(socks5Url, "SOCKS5 链接"));
      $("#copyHttpBtn")?.addEventListener?.("click", () => doCopy(httpUrl, "HTTP 链接"));
    })
  );

  $$("[data-logs]").forEach((btn) =>
    btn.addEventListener("click", async () => {
      try {
        const id = btn.dataset.logs;
        const inst = instances.find((i) => i.id === id);
        const title = inst ? `日志 · ${inst.name}` : "实例日志";
        const { lines } = await api(`/api/instances/${id}/logs`);
        const text = (lines || []).join("\n");
        openModal({
          title,
          actionsHtml: `<button class="btn sm" id="copyModalText" type="button">复制</button>`,
          bodyHtml: `<textarea id="modalText" readonly>${escapeHtml(text || "（暂无日志）")}</textarea>`
        });
        $("#copyModalText")?.addEventListener?.("click", async () => {
          try {
            await navigator.clipboard.writeText(text);
            toast("已复制");
          } catch {
            toast("复制失败（浏览器权限限制）", false);
          }
        });
      } catch (e) {
        toast(e.message, false);
      }
    })
  );
}

async function renderPool() {
  const el = $("#view-pool");
  const [{ proxies }, { settings }] = await Promise.all([api("/api/pool"), api("/api/settings")]);
  const exportHost = String(settings?.exportHost || "").trim();

  const runningCount = (proxies || []).filter((p) => p.running).length;
  const hostPreview =
    exportHost ||
    (proxies?.length ? String(proxies[0]?.proxy ?? "").slice(0, String(proxies[0]?.proxy ?? "").lastIndexOf(":")) || "-" : "-");
  const text = (proxies || [])
    .map((p) => `${p.proxy}\t${p.running ? "运行中" : "已停止"}\t${p.name}`)
    .join("\n");

  el.innerHTML = `
    ${pageHeader("代理池", "导出每个实例的 host:port（一个端口一个出口）。", `<button class="btn primary" id="copyPool">复制列表</button>`)}

    <div class="grid">
      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">代理列表</div>
            <div class="panel-subtitle">每行：<code>proxy</code> / <code>状态</code> / <code>名称</code>（Tab 分隔）。</div>
          </div>
        </div>
        <textarea id="poolText" readonly>${escapeHtml(text || "")}</textarea>
      </div>

      <div class="panel">
        <div class="panel-header">
          <div>
            <div class="panel-title">概览与说明</div>
            <div class="panel-subtitle">用于外部工具快速导入与批量轮换端口。</div>
          </div>
        </div>

        <div class="stats">
          <div class="stat">
            <div class="stat-value">${proxies.length}</div>
            <div class="stat-label">代理数量（实例数）</div>
          </div>
          <div class="stat">
            <div class="stat-value" style="color: var(--ok)">${runningCount}</div>
            <div class="stat-label">运行中代理</div>
          </div>
          <div class="stat">
            <div class="stat-value" style="color: var(--danger)">${proxies.length - runningCount}</div>
            <div class="stat-label">已停止代理</div>
          </div>
          <div class="stat">
            <div class="stat-value">${escapeHtml(hostPreview)}</div>
            <div class="stat-label">导出 host（来自「设置」）</div>
          </div>
        </div>

        <div class="help" style="margin-top:10px">
          说明：导出列表使用「设置」里的「导出 Host」。首次启动若为空，服务会尝试自动获取公网 IP 并写入；也可以在「设置」里手动填写或点击「自动获取公网 IP」。<br/>
          提示：实例的实际监听由「设置」里的 <code>bind-address</code> / <code>allow-lan</code> 控制；公网访问还需放行端口（防火墙/安全组/端口映射）。
        </div>
      </div>
    </div>
  `;

  $("#copyPool").addEventListener("click", async () => {
    const ok = await copyToClipboard($("#poolText").value);
    if (ok) toast("已复制");
    else toast("复制失败（浏览器权限限制）", false);
  });
}

async function render() {
  if (!getToken()) {
    renderLogin();
    return;
  }

  setNavVisible(true);
  const active = $(".tab.active").dataset.tab;
  try {
    if (active === "settings") return await renderSettings();
    if (active === "subscriptions") return await renderSubscriptions();
    if (active === "pool") return await renderPool();
    return await renderInstances();
  } catch (e) {
    toast(e.message || "渲染失败", false);
    if (!getToken()) renderLogin();
  }
}

render();
