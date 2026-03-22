(() => {
  const requestLoadingButton = new WeakMap();
  const buttonInitialDisabled = new WeakMap();
  const modalCloseDelayMs = 180;
  const toastCloseDelayMs = 160;
  const toastDefaultDurationMs = 2600;
  let pendingScrollRestoreState = null;

  function findLoadingButton(elt) {
    if (!elt || !elt.closest) return null;
    if (elt.matches && elt.matches("[data-loading-button]")) return elt;
    if (elt.matches && elt.matches("form[data-loading-submit]")) {
      const selector = String(elt.getAttribute("data-loading-submit") || "").trim();
      if (selector && selector !== "true") {
        const selected = elt.querySelector(selector);
        if (selected) return selected;
      }
      return elt.querySelector("button[type='submit'][data-loading-button], button[type='submit']");
    }
    const form = elt.closest("form[data-loading-submit]");
    if (form) {
      const selector = String(form.getAttribute("data-loading-submit") || "").trim();
      if (selector && selector !== "true") {
        const selected = form.querySelector(selector);
        if (selected) return selected;
      }
      return form.querySelector("button[type='submit'][data-loading-button], button[type='submit']");
    }
    return elt.closest("[data-loading-button]");
  }

  function setButtonLoading(button, loading) {
    if (!button) return;
    if (loading) {
      if (!buttonInitialDisabled.has(button)) {
        buttonInitialDisabled.set(button, !!button.disabled);
      }
      button.disabled = true;
      button.classList.add("is-loading");
      button.setAttribute("aria-busy", "true");
      return;
    }

    const initialDisabled = buttonInitialDisabled.has(button) ? buttonInitialDisabled.get(button) : false;
    buttonInitialDisabled.delete(button);
    if (!button.isConnected) return;
    button.disabled = !!initialDisabled;
    button.classList.remove("is-loading");
    button.removeAttribute("aria-busy");
  }

  function getToastRoot() {
    let root = document.getElementById("ui-toast-root");
    if (root) return root;

    root = document.createElement("div");
    root.id = "ui-toast-root";
    root.className = "toast-root";
    root.setAttribute("aria-live", "polite");
    root.setAttribute("aria-atomic", "true");
    document.body.appendChild(root);
    return root;
  }

  function collectNodes(scope, selector) {
    if (!scope) return [];
    const out = [];
    if (scope.matches && scope.matches(selector)) out.push(scope);
    if (scope.querySelectorAll) {
      scope.querySelectorAll(selector).forEach((node) => out.push(node));
    }
    return out;
  }

  function createSubscriptionHealthBadge(label, toneClass) {
    const badge = document.createElement("span");
    badge.className = toneClass ? "badge " + toneClass : "badge";
    badge.textContent = label;
    return badge;
  }

  function updateSubscriptionProxyHealthCell(cell, result) {
    if (!cell) return;
    cell.textContent = "";

    if (!result || typeof result !== "object") {
      cell.appendChild(createSubscriptionHealthBadge("未检测", ""));
      return;
    }

    if (result.ok) {
      cell.appendChild(createSubscriptionHealthBadge("可用", "ok"));
      const latency = Number(result.latencyMs);
      cell.appendChild(document.createTextNode(" " + (Number.isFinite(latency) && latency > 0 ? Math.round(latency) + "ms" : "-")));
      return;
    }

    cell.appendChild(createSubscriptionHealthBadge("不可用", "bad"));
    const message = String(result.error || "检测失败").trim();
    if (message) {
      cell.appendChild(document.createTextNode(" " + message));
    }
  }

  async function requestSubscriptionProxyCheck(checkURL, proxyName) {
    const response = await fetch(checkURL, {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "content-type": "application/json",
      },
      body: JSON.stringify({ "proxyName": proxyName }),
    });

    let payload = null;
    try {
      payload = await response.json();
    } catch {}

    if (!response.ok) {
      throw new Error(String((payload && payload.error) || ("HTTP " + response.status)));
    }
    if (!payload || payload.ok === false) {
      throw new Error(String((payload && payload.error) || "检测失败"));
    }

    const results = payload.results && typeof payload.results === "object" ? payload.results : null;
    const result = results ? results[proxyName] : null;
    if (!result || typeof result !== "object") {
      throw new Error("检测结果缺失");
    }
    return result;
  }

  function closeSelectMenus(except) {
    document.querySelectorAll("[data-ui-select].is-open").forEach((node) => {
      if (except && node === except) return;
      node.classList.remove("is-open");
      const trigger = node.querySelector("[data-ui-select-trigger]");
      if (trigger) trigger.setAttribute("aria-expanded", "false");
    });
  }

  function openSelectMenu(select) {
    if (!select) return;
    closeSelectMenus(select);
    select.classList.add("is-open");
    const trigger = select.querySelector("[data-ui-select-trigger]");
    if (trigger) trigger.setAttribute("aria-expanded", "true");
  }

  function syncSelectOption(select, option) {
    if (!select || !option) return;
    const value = String(option.getAttribute("data-value") || option.value || "");
    const label = String(option.getAttribute("data-label") || option.textContent || "").trim();
    const input = select.querySelector("[data-ui-select-input]");
    const labelNode = select.querySelector("[data-ui-select-label]");
    if (input) input.value = value;
    if (labelNode) labelNode.textContent = label;

    select.querySelectorAll("[data-ui-select-option]").forEach((node) => {
      const active = node === option;
      node.classList.toggle("is-selected", active);
      node.setAttribute("aria-selected", active ? "true" : "false");
    });
  }

  function dismissToastNode(toast) {
    if (!toast || toast.dataset.toastClosing === "true") return;
    toast.dataset.toastClosing = "true";
    if (toast.dataset.toastTimer) {
      window.clearTimeout(Number(toast.dataset.toastTimer));
      delete toast.dataset.toastTimer;
    }
    toast.classList.remove("is-open");
    toast.classList.add("is-closing");
    window.setTimeout(() => {
      if (toast.parentNode) toast.parentNode.removeChild(toast);
    }, toastCloseDelayMs);
  }

  function primeToast(toast) {
    if (!toast || toast.dataset.toastReady === "true") return;
    toast.dataset.toastReady = "true";
    window.requestAnimationFrame(() => {
      toast.classList.add("is-open");
    });

    const autoHideMs = Number(toast.getAttribute("data-toast-autohide") || toastDefaultDurationMs);
    if (autoHideMs > 0) {
      const timerId = window.setTimeout(() => {
        dismissToastNode(toast);
      }, autoHideMs);
      toast.dataset.toastTimer = String(timerId);
    }
  }

  function primePendingToasts(scope) {
    collectNodes(scope || document, "[data-toast]").forEach((toast) => {
      primeToast(toast);
    });
  }

  function closeModalElement(modal) {
    if (!modal || modal.dataset.modalClosing === "true") return;
    modal.dataset.modalClosing = "true";
    modal.classList.remove("is-open");
    modal.classList.add("is-closing");
    window.setTimeout(() => {
      if (modal.parentNode) modal.parentNode.removeChild(modal);
    }, modalCloseDelayMs);
  }

  function armModal(modal) {
    if (!modal || modal.dataset.modalReady === "true") return;
    modal.dataset.modalReady = "true";
    window.requestAnimationFrame(() => {
      modal.classList.add("is-open");
    });
  }

  function primePendingModals(scope) {
    collectNodes(scope || document, ".modal").forEach((modal) => {
      armModal(modal);
    });
  }

  function removeTopModal() {
    const modals = document.querySelectorAll(".modal");
    if (!modals || modals.length === 0) return false;
    closeModalElement(modals[modals.length - 1]);
    return true;
  }

  function buildConfirmModal(title, message) {
    const modal = document.createElement("div");
    modal.className = "modal";
    modal.setAttribute("role", "dialog");
    modal.setAttribute("aria-modal", "true");
    modal.setAttribute("data-confirm-modal", "true");
    modal.addEventListener("click", window.proxyPoolModalBackdropClose);

    const card = document.createElement("div");
    card.className = "panel modal-card";
    card.addEventListener("click", (evt) => {
      evt.stopPropagation();
    });

    const header = document.createElement("div");
    header.className = "modal-header";

    const heading = document.createElement("div");
    const titleNode = document.createElement("div");
    titleNode.className = "modal-title";
    titleNode.textContent = title;
    heading.appendChild(titleNode);

    const subtitleNode = document.createElement("div");
    subtitleNode.className = "panel-subtitle";
    subtitleNode.textContent = message;
    heading.appendChild(subtitleNode);
    header.appendChild(heading);

    const actions = document.createElement("div");
    actions.className = "modal-actions";

    const cancelButton = document.createElement("button");
    cancelButton.className = "btn sm";
    cancelButton.type = "button";
    cancelButton.textContent = "取消";
    cancelButton.addEventListener("click", () => {
      closeModalElement(modal);
    });
    actions.appendChild(cancelButton);

    header.appendChild(actions);

    const body = document.createElement("div");
    body.className = "modal-body";

    const footer = document.createElement("div");
    footer.className = "modal-form-footer";

    const confirmButton = document.createElement("button");
    confirmButton.className = "btn danger";
    confirmButton.type = "button";
    confirmButton.textContent = "确认";
    footer.appendChild(confirmButton);
    body.appendChild(footer);

    card.appendChild(header);
    card.appendChild(body);
    modal.appendChild(card);

    return { modal, confirmButton };
  }

  function onBeforeRequest(evt) {
    const elt = evt && evt.detail ? evt.detail.elt : null;
    if (elt && elt.closest && elt.closest("[data-preserve-scroll]")) {
      const target = evt && evt.detail ? evt.detail.target : null;
      if (target && target.id === "ui-extra") {
        const scrollHost = elt.closest(".modal-body");
        pendingScrollRestoreState = {
          kind: scrollHost ? "element" : "window",
          targetId: "ui-extra",
          top: scrollHost ? scrollHost.scrollTop : window.scrollY || window.pageYOffset || 0,
        };
      } else {
        pendingScrollRestoreState = {
          kind: "window",
          targetId: target && target.id ? target.id : "ui-tab",
          top: window.scrollY || window.pageYOffset || 0,
        };
      }
    }

    const btn = findLoadingButton(elt);
    if (!btn) return;
    if (elt) requestLoadingButton.set(elt, btn);
    setButtonLoading(btn, true);
  }

  function onAfterRequest(evt) {
    const elt = evt && evt.detail ? evt.detail.elt : null;
    let btn = null;
    if (elt) {
      btn = requestLoadingButton.get(elt) || null;
      requestLoadingButton.delete(elt);
    }
    if (!btn) btn = findLoadingButton(elt);
    setButtonLoading(btn, false);
  }

  function onAfterSwap(evt) {
    const target = evt && evt.detail ? evt.detail.target : null;
    closeSelectMenus();
    primePendingToasts(document);
    primePendingModals(target || document);

    if (!target || !target.id || pendingScrollRestoreState === null) return;
    if (target.id !== "ui-tab" && target.id !== "ui-extra") return;
    if (target.id !== pendingScrollRestoreState.targetId) return;

    const restoreState = pendingScrollRestoreState;
    pendingScrollRestoreState = null;
    window.requestAnimationFrame(() => {
      if (restoreState.kind === "element" && target.id === "ui-extra") {
        const scrollHost = target.querySelector(".modal-body");
        if (scrollHost) {
          scrollHost.scrollTop = restoreState.top;
          return;
        }
      }
      window.scrollTo({ top: restoreState.top, behavior: "auto" });
    });
  }

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

  window.proxyPoolDismissToast = function (el) {
    const toast = el && el.closest ? el.closest(".toast") : null;
    dismissToastNode(toast);
  };

  window.proxyPoolShowToast = function (message, tone, options) {
    const text = String(message || "").trim();
    if (!text) return null;

    const toast = document.createElement("div");
    toast.className = "toast " + (tone === "error" ? "toast-error" : "toast-success");
    toast.setAttribute("data-toast", "");
    toast.setAttribute("role", tone === "error" ? "alert" : "status");

    const duration = options && typeof options.duration === "number" ? options.duration : toastDefaultDurationMs;
    if (duration > 0) {
      toast.setAttribute("data-toast-autohide", String(duration));
    }

    const content = document.createElement("div");
    content.className = "toast-content";

    const messageNode = document.createElement("div");
    messageNode.className = "toast-message";
    messageNode.textContent = text;
    content.appendChild(messageNode);

    const closeButton = document.createElement("button");
    closeButton.className = "btn ghost sm toast-close";
    closeButton.type = "button";
    closeButton.textContent = "关闭";
    closeButton.addEventListener("click", () => {
      dismissToastNode(toast);
    });

    toast.appendChild(content);
    toast.appendChild(closeButton);

    const root = getToastRoot();
    root.prepend(toast);
    primeToast(toast);
    return toast;
  };

  window.proxyPoolCopyText = async function (text, successMessage, failureMessage) {
    const ok = await copyText(text);
    if (ok) {
      window.proxyPoolShowToast(successMessage || "已复制", "success");
    } else {
      window.proxyPoolShowToast(failureMessage || "复制失败（浏览器权限限制）", "error");
    }
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

  window.proxyPoolRunSubscriptionBulkCheck = async function (button) {
    const trigger = button && button.closest ? button.closest("[data-subscription-bulk-check]") : null;
    const modal = trigger && trigger.closest ? trigger.closest(".modal") : null;
    if (!trigger || !modal) return false;
    if (modal.getAttribute("data-subscription-bulk-running") === "true") return false;

    const defaultCheckURL = String(trigger.getAttribute("data-subscription-proxy-check-url") || "").trim();
    const rows = Array.from(modal.querySelectorAll("tr[data-subscription-proxy-name]"));
    if (!defaultCheckURL || rows.length === 0) return false;

    const loadingTextNode = trigger.querySelector(".btn-loading-text");
    const originalLoadingText = loadingTextNode ? loadingTextNode.textContent : "检测中...";
    modal.setAttribute("data-subscription-bulk-running", "true");
    setButtonLoading(trigger, true);

    try {
      for (let idx = 0; idx < rows.length; idx++) {
        if (!modal.isConnected || modal.getAttribute("data-subscription-bulk-running") !== "true") break;

        const row = rows[idx];
        const proxyName = String(row.getAttribute("data-subscription-proxy-name") || "").trim();
        const rowButton = row.querySelector("button.subscription-proxy-check-btn");
        const healthCell = row.querySelector("[data-subscription-proxy-health]");
        const checkURL = String((rowButton && rowButton.getAttribute("data-subscription-proxy-check-url")) || defaultCheckURL).trim();
        if (!proxyName || !rowButton || !healthCell || !checkURL) continue;

        rowButton.setAttribute("data-subscription-bulk-current", "true");
        if (loadingTextNode) {
          loadingTextNode.textContent = "检测中（" + (idx + 1) + "/" + rows.length + "）";
        }
        setButtonLoading(rowButton, true);

        try {
          const result = await requestSubscriptionProxyCheck(checkURL, proxyName);
          updateSubscriptionProxyHealthCell(healthCell, result);
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err || "检测失败");
          updateSubscriptionProxyHealthCell(healthCell, { ok: false, error: message });
          window.proxyPoolShowToast("节点 " + proxyName + " 检测请求失败：" + message, "error");
          break;
        } finally {
          rowButton.removeAttribute("data-subscription-bulk-current");
          setButtonLoading(rowButton, false);
        }
      }
    } finally {
      modal.removeAttribute("data-subscription-bulk-running");
      if (loadingTextNode) {
        loadingTextNode.textContent = originalLoadingText;
      }
      setButtonLoading(trigger, false);
    }

    return false;
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

  window.proxyPoolCloseModal = function (el) {
    if (el && el.closest) {
      const modal = el.closest(".modal");
      if (modal) {
        closeModalElement(modal);
        return;
      }
    }
    removeTopModal();
  };

  window.proxyPoolModalBackdropClose = function (evt) {
    if (!evt || !evt.target || !evt.target.classList) return;
    if (evt.target.classList.contains("modal")) {
      window.proxyPoolCloseModal(evt.target);
    }
  };

  window.proxyPoolConfirmAction = function (button) {
    if (!button) return false;

    document.querySelectorAll(".modal[data-confirm-modal='true']").forEach((modal) => {
      if (modal.parentNode) modal.parentNode.removeChild(modal);
    });

    const title = String(button.getAttribute("data-confirm-title") || "请确认").trim();
    const message = String(button.getAttribute("data-confirm-message") || "确认继续该操作？").trim();
    const confirm = buildConfirmModal(title, message);
    document.body.appendChild(confirm.modal);
    armModal(confirm.modal);

    confirm.confirmButton.addEventListener("click", () => {
      closeModalElement(confirm.modal);
      if (window.htmx && typeof window.htmx.trigger === "function") {
        htmx.trigger(button, "confirmed");
      }
    });

    window.requestAnimationFrame(() => {
      confirm.confirmButton.focus({ preventScroll: true });
    });
    return false;
  };

  document.addEventListener("keydown", (evt) => {
    if (!evt || evt.key !== "Escape") return;
    if (document.querySelector("[data-ui-select].is-open")) {
      closeSelectMenus();
      return;
    }
    removeTopModal();
  });

  document.addEventListener("click", (evt) => {
    const target = evt && evt.target ? evt.target : null;
    if (!target || !target.closest) {
      closeSelectMenus();
      return;
    }

    const proxyCheckButton = target.closest(".subscription-proxy-check-btn");
    if (proxyCheckButton) {
      const modal = proxyCheckButton.closest(".modal");
      if (modal && modal.getAttribute("data-subscription-bulk-running") === "true" && proxyCheckButton.getAttribute("data-subscription-bulk-current") !== "true") {
        evt.preventDefault();
        evt.stopPropagation();
        return;
      }
    }

    const option = target.closest("[data-ui-select-option]");
    if (option) {
      const select = option.closest("[data-ui-select]");
      if (!select) return;
      syncSelectOption(select, option);
      closeSelectMenus();
      if (select.getAttribute("data-ui-select-submit") === "true") {
        const form = select.closest("form");
        if (form && typeof form.requestSubmit === "function") {
          form.requestSubmit();
        }
      }
      return;
    }

    const trigger = target.closest("[data-ui-select-trigger]");
    if (trigger) {
      const select = trigger.closest("[data-ui-select]");
      if (!select) return;
      if (select.classList.contains("is-open")) {
        closeSelectMenus();
      } else {
        openSelectMenu(select);
      }
      return;
    }

    if (!target.closest("[data-ui-select]")) {
      closeSelectMenus();
    }
  }, true);

  primePendingToasts(document);
  primePendingModals(document);

  document.body.addEventListener("htmx:beforeRequest", onBeforeRequest);
  document.body.addEventListener("htmx:afterRequest", onAfterRequest);
  document.body.addEventListener("htmx:afterSwap", onAfterSwap);
  document.body.addEventListener("htmx:responseError", onAfterRequest);
  document.body.addEventListener("htmx:sendError", onAfterRequest);
  document.body.addEventListener("htmx:timeout", onAfterRequest);
})();
