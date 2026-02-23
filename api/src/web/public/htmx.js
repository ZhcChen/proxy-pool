(() => {
  async function copyText(raw) {
    const text = String(raw ?? "");
    if (!text.trim()) return false;
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text);
        return true;
      }
    } catch {}
    try {
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.setAttribute("readonly", "readonly");
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      ta.setSelectionRange(0, ta.value.length);
      const ok = document.execCommand("copy");
      document.body.removeChild(ta);
      return !!ok;
    } catch {
      return false;
    }
  }

  window.proxyPoolCopyText = async function (text) {
    const ok = await copyText(text);
    window.alert(ok ? "已复制" : "复制失败（浏览器权限限制）");
    return ok;
  };

  window.proxyPoolFilterTableRows = function (tableId, keyword) {
    const table = document.getElementById(tableId);
    if (!table) return;
    const kw = String(keyword || "").trim().toLowerCase();
    const rows = table.querySelectorAll("tbody tr[data-proxy-name]");
    rows.forEach((row) => {
      const name = String(row.getAttribute("data-proxy-name") || "");
      if (!kw || name.includes(kw)) {
        row.style.display = "";
      } else {
        row.style.display = "none";
      }
    });
  };

  window.proxyPoolSetActiveTab = function (el) {
    const tab = el && el.closest ? el.closest(".tab") : null;
    if (!tab) return;
    const nav = tab.closest(".nav");
    if (!nav) return;

    nav.querySelectorAll(".tab.active").forEach((node) => {
      node.classList.remove("active");
      node.removeAttribute("aria-current");
    });

    tab.classList.add("active");
    tab.setAttribute("aria-current", "page");
  };
})();
