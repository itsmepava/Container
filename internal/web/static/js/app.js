// Eimsky web behaviours. Plain JS, no framework; htmx handles partial updates.
(function () {
  "use strict";
  var csrf = (document.querySelector('meta[name="csrf-token"]') || {}).content || "";

  // htmx: send the CSRF token and keep focus/scroll stable.
  document.addEventListener("htmx:configRequest", function (e) {
    if (csrf) e.detail.headers["X-CSRF-Token"] = csrf;
  });

  // Keep the scroll position across full-page form posts.
  var scrollKey = "scroll:" + location.pathname;
  try {
    var saved = sessionStorage.getItem(scrollKey);
    if (saved !== null) { sessionStorage.removeItem(scrollKey); window.scrollTo(0, parseInt(saved, 10) || 0); }
  } catch (_) {}
  document.addEventListener("submit", function (e) {
    var f = e.target;
    var msg = f.getAttribute("data-confirm");
    if (msg && !window.confirm(msg)) { e.preventDefault(); return; }
    if (f.hasAttribute("data-keep-scroll")) { try { sessionStorage.setItem(scrollKey, String(window.scrollY)); } catch (_) {} }
    // Prevent double submits.
    var btns = f.querySelectorAll('button[type=submit],button:not([type])');
    setTimeout(function () { btns.forEach(function (b) { b.disabled = true; }); }, 0);
  });

  // Auto-submit selects and file inputs.
  document.addEventListener("change", function (e) {
    var el = e.target;
    if (el.matches("[data-autosubmit]")) {
      var f = el.form;
      if (f && f.hasAttribute("data-keep-scroll")) { try { sessionStorage.setItem(scrollKey, String(window.scrollY)); } catch (_) {} }
      if (f) { if (f.requestSubmit) f.requestSubmit(); else f.submit(); }
    }
    if (el.matches(".radio-card input")) {
      document.querySelectorAll('input[name="' + el.name + '"]').forEach(function (r) {
        var c = r.closest(".radio-card"); if (c) c.classList.toggle("on", r.checked);
      });
    }
  });

  // Countdowns: data-countdown="RFC3339" shows "5 d", "1 d 6 h", "40 min".
  function until(t) {
    var d = (new Date(t) - new Date()) / 1000;
    if (d <= 0) return "passed";
    var days = Math.floor(d / 86400), h = Math.floor((d % 86400) / 3600), m = Math.floor((d % 3600) / 60);
    if (days >= 2) return days + " d";
    if (days >= 1) return days + " d " + h + " h";
    if (h >= 1) return h + " h " + m + " min";
    return (m + 1) + " min";
  }
  function mmss(t) {
    var d = Math.max(0, Math.floor((new Date(t) - new Date()) / 1000));
    var m = Math.floor(d / 60), s = d % 60;
    return m + ":" + (s < 10 ? "0" : "") + s;
  }
  function tick() {
    document.querySelectorAll("[data-countdown]").forEach(function (el) { el.textContent = until(el.getAttribute("data-countdown")); });
    document.querySelectorAll("[data-timer]").forEach(function (el) {
      var left = mmss(el.getAttribute("data-timer"));
      el.textContent = left;
      if (left === "0:00" && !el.dataset.done) { el.dataset.done = "1"; var r = el.getAttribute("data-expired"); if (r) location.reload(); }
    });
  }
  tick(); setInterval(tick, 1000);

  // Demo logins on the sign-in page.
  document.addEventListener("click", function (e) {
    var b = e.target.closest("[data-fill-email]");
    if (!b) return;
    e.preventDefault();
    var f = b.closest("form") || document.querySelector("form");
    document.querySelector('input[name="email"]').value = b.getAttribute("data-fill-email");
    document.querySelector('input[name="password"]').value = b.getAttribute("data-fill-password");
  });

  // Dialogs: data-open="id" opens <dialog id>, data-close closes.
  document.addEventListener("click", function (e) {
    var o = e.target.closest("[data-open]");
    if (o) { e.preventDefault(); var d = document.getElementById(o.getAttribute("data-open")); if (d && d.showModal) d.showModal(); return; }
    var c = e.target.closest("[data-close]");
    if (c) { e.preventDefault(); var dlg = c.closest("dialog"); if (dlg) dlg.close(); }
  });

  // Clickable rows.
  document.addEventListener("click", function (e) {
    var tr = e.target.closest("tr[data-href]");
    if (tr && !e.target.closest("a,button,input,select,label,form")) location.href = tr.getAttribute("data-href");
  });

  // ---------- Booking form: cargo lines ----------
  var cargo = document.getElementById("cargo");
  if (cargo) initCargo(cargo);

  function initCargo(table) {
    var body = table.querySelector("tbody");
    var tpl = document.getElementById("cargo-row");
    var form = table.closest("form");
    function num(v) { var n = parseFloat(String(v).replace(/,/g, "")); return isFinite(n) ? n : 0; }
    function fmt(n, d) { return n.toLocaleString("en-US", { minimumFractionDigits: d, maximumFractionDigits: d }); }
    function recalc() {
      var pieces = 0, cbm = 0, kg = 0;
      body.querySelectorAll("tr").forEach(function (tr, i) {
        var g = function (n) { var el = tr.querySelector('[name="' + n + '"]'); return el ? num(el.value) : 0; };
        var p = g("pieces"), l = g("length"), w = g("width"), h = g("height"), k = g("kg");
        var c = Math.round(p * l * w * h / 1e6 * 100) / 100;
        tr.querySelector(".cbm").textContent = c ? fmt(c, 2) : "–";
        pieces += p; cbm += c; kg += p * k;
        var rm = tr.querySelector(".rm"); if (rm) rm.style.visibility = body.children.length > 1 ? "visible" : "hidden";
      });
      var set = function (id, v) { var el = document.getElementById(id); if (el) el.textContent = v; };
      set("t-pieces", pieces ? fmt(pieces, 0) : "–");
      set("t-kg", kg ? fmt(Math.round(kg), 0) + " kg" : "–");
      set("t-cbm", cbm ? fmt(cbm, 2) : "–");
      var bookBtn = document.getElementById("book-cbm"); if (bookBtn) bookBtn.textContent = cbm ? fmt(cbm, 2).replace(/\.?0+$/, "") : "0";
    }
    function addRow(focus) {
      var row = tpl.content.firstElementChild.cloneNode(true);
      body.appendChild(row);
      recalc();
      if (focus) { var f = row.querySelector("input"); if (f) f.focus(); }
      return row;
    }
    document.getElementById("add-line").addEventListener("click", function (e) { e.preventDefault(); addRow(true); });
    body.addEventListener("click", function (e) {
      var rm = e.target.closest(".rm");
      if (rm && body.children.length > 1) { rm.closest("tr").remove(); recalc(); htmx.trigger(form, "cargo-changed"); }
    });
    body.addEventListener("input", recalc);
    // Enter in the last cell adds a new line.
    body.addEventListener("keydown", function (e) {
      if (e.key === "Enter" && e.target.matches("input")) {
        e.preventDefault();
        var tr = e.target.closest("tr");
        var inputs = Array.prototype.slice.call(tr.querySelectorAll("input"));
        var i = inputs.indexOf(e.target);
        if (i < inputs.length - 1) inputs[i + 1].focus();
        else if (tr === body.lastElementChild) addRow(true);
        else tr.nextElementSibling.querySelector("input").focus();
      }
    });
    if (!body.children.length) addRow(false);
    recalc();

    // HS code search.
    var list = null, timer = null, active = -1;
    function close() { if (list) { list.remove(); list = null; active = -1; } }
    body.addEventListener("input", function (e) {
      if (!e.target.matches(".hs-input")) return;
      var input = e.target;
      clearTimeout(timer);
      timer = setTimeout(function () {
        var q = input.value.trim();
        if (q.length < 2) { close(); return; }
        fetch("/hs?q=" + encodeURIComponent(q), { headers: { Accept: "application/json" } }).then(function (r) { return r.json(); }).then(function (items) {
          close();
          if (!items.length || document.activeElement !== input) return;
          list = document.createElement("div");
          list.className = "hs-list";
          items.forEach(function (it) {
            var b = document.createElement("button");
            b.type = "button";
            b.innerHTML = '<span class="c"></span><span class="d"></span>';
            b.querySelector(".c").textContent = it.code;
            b.querySelector(".d").textContent = it.description;
            b.addEventListener("mousedown", function (ev) { ev.preventDefault(); input.value = it.code; close(); input.dispatchEvent(new Event("change", { bubbles: true })); var next = input.closest("tr").querySelector('[name="pieces"]'); if (next) next.focus(); });
            list.appendChild(b);
          });
          input.parentNode.appendChild(list);
        }).catch(function () {});
      }, 180);
    });
    body.addEventListener("keydown", function (e) {
      if (!list || !e.target.matches(".hs-input")) return;
      var items = list.querySelectorAll("button");
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        active = (active + (e.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
        items.forEach(function (b, i) { b.classList.toggle("on", i === active); });
      } else if (e.key === "Enter" && active >= 0) {
        e.preventDefault(); e.stopImmediatePropagation();
        items[active].dispatchEvent(new MouseEvent("mousedown"));
      } else if (e.key === "Escape") close();
    }, true);
    body.addEventListener("focusout", function (e) { if (e.target.matches(".hs-input")) setTimeout(close, 150); });
  }

  // Planner: hovering a lot highlights its row and vice versa.
  document.addEventListener("mouseover", function (e) {
    var el = e.target.closest("[data-lot]");
    document.querySelectorAll(".hl").forEach(function (x) { x.classList.remove("hl"); });
    if (!el) return;
    document.querySelectorAll('[data-lot="' + el.getAttribute("data-lot") + '"]').forEach(function (x) { x.classList.add("hl"); });
  });
})();

// Navigation selects: <select data-nav> with URL option values.
document.addEventListener("change", function (e) {
  if (e.target.matches("select[data-nav]")) location.href = e.target.value;
});
