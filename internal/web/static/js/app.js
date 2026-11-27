// Eimsky web behaviours. Plain JS, no framework; htmx handles partial updates.
(function () {
  "use strict";

  // ---------- Theme ----------
  var html = document.documentElement;
  function setTheme(t) {
    html.setAttribute("data-theme", t);
    document.cookie = "theme=" + t + "; path=/; max-age=31536000; samesite=lax";
    document.querySelectorAll("[data-theme-set]").forEach(function (b) { b.classList.toggle("on", b.getAttribute("data-theme-set") === t); });
    var c = document.querySelector("[data-theme-cycle]"); if (c) c.title = "Theme: " + t;
  }
  document.addEventListener("click", function (e) {
    var set = e.target.closest("[data-theme-set]");
    if (set) { e.preventDefault(); setTheme(set.getAttribute("data-theme-set")); return; }
    var cyc = e.target.closest("[data-theme-cycle]");
    if (cyc) {
      e.preventDefault();
      var order = ["light", "dark", "system"], cur = html.getAttribute("data-theme") || "system";
      setTheme(order[(order.indexOf(cur) + 1) % order.length]);
    }
  });

  // ---------- Sidebar drawer (phones) and menus ----------
  document.addEventListener("click", function (e) {
    if (e.target.closest("[data-nav-toggle]")) { document.body.classList.toggle("nav-open"); return; }
    if (e.target.closest("[data-nav-close]")) { document.body.classList.remove("nav-open"); return; }
    document.querySelectorAll("details.menu[open]").forEach(function (d) { if (!d.contains(e.target)) d.removeAttribute("open"); });
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") {
      document.body.classList.remove("nav-open");
      document.querySelectorAll("details.menu[open]").forEach(function (d) { d.removeAttribute("open"); });
    }
  });

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
    // Prevent double submits, but never leave buttons dead: they come back
    // after a few seconds, and when the page is restored with Back/Forward.
    var btns = f.querySelectorAll('button[type=submit],button:not([type])');
    setTimeout(function () { btns.forEach(function (b) { b.disabled = true; b.classList.add("busy"); b.dataset.busy = "1"; }); }, 0);
    setTimeout(function () { releaseButtons(f); }, 8000);
  });
  function releaseButtons(root) {
    (root || document).querySelectorAll('[data-busy]').forEach(function (b) { b.disabled = false; b.classList.remove("busy"); delete b.dataset.busy; });
  }
  window.addEventListener("pageshow", function (e) { if (e.persisted) releaseButtons(); });

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
    document.querySelectorAll(".fig.dim").forEach(function (x) { x.classList.remove("dim"); });
    if (!el || el.getAttribute("data-lot") === "0") return;
    document.querySelectorAll('[data-lot="' + el.getAttribute("data-lot") + '"]').forEach(function (x) {
      x.classList.add("hl");
      var svg = x.closest(".fig");
      if (svg) svg.classList.add("dim");
    });
  });
})();

// Navigation selects: <select data-nav> with URL option values.
document.addEventListener("change", function (e) {
  if (e.target.matches("select[data-nav]")) location.href = e.target.value;
});

// New sailing: derive plan lock and bid close from the CFS cut-off.
document.addEventListener("change", function (e) {
  var c = e.target;
  if (!c.matches("[data-derive-lock]")) return;
  var t = new Date(c.value);
  if (isNaN(t)) return;
  function fmt(d) { var p = function (n) { return String(n).padStart(2, "0"); }; return d.getFullYear() + "-" + p(d.getMonth() + 1) + "-" + p(d.getDate()) + "T" + p(d.getHours()) + ":" + p(d.getMinutes()); }
  var lock = document.querySelector('[name="lock"]'), close = document.querySelector('[name="close"]');
  if (lock) lock.value = fmt(new Date(t - c.getAttribute("data-derive-lock") * 3600e3));
  if (close) close.value = fmt(new Date(t - c.getAttribute("data-derive-close") * 3600e3));
});

// Container drawings: oblique 3D, to true scale, as the load planner shows
// them. Each .fig[data-box] carries the container and its lots as JSON.
(function () {
  function f1(v) { return (Math.round(v * 10) / 10).toFixed(1); }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]; }); }
  function draw(svg, p, s) {
    var W = svg.clientWidth; if (!W) return;
    var H = svg.clientHeight;
    var dx = p.W * s * .42, dy = p.W * s * .28, x0 = 12, y0 = dy + 14, Ls = p.L * s, Hs = p.H * s, perCBM = p.usable > 0 ? Ls / p.usable : 0;
    var g = '<polygon class="bx-top" points="' + x0 + ',' + y0 + ' ' + (x0 + Ls) + ',' + y0 + ' ' + (x0 + Ls + dx) + ',' + (y0 - dy) + ' ' + (x0 + dx) + ',' + (y0 - dy) + '"/>' +
      '<rect class="bx-front" x="' + x0 + '" y="' + y0 + '" width="' + Ls + '" height="' + Hs + '"/>';
    for (var m = 0.3; m < p.L; m += 0.3) g += '<line class="bx-rib" x1="' + (x0 + m * s) + '" x2="' + (x0 + m * s) + '" y1="' + (y0 + 2) + '" y2="' + (y0 + Hs - 2) + '"/>';
    var a = 0, used = 0;
    (p.lots || []).forEach(function (l) {
      var len = Math.min(l.c * perCBM, Ls - a), c = l.col, x = x0 + a, ink = l.ink || "#fff";
      if (len <= 0) return;
      g += '<g class="bx-lot" data-lot="' + (l.id || 0) + '"><title>' + esc(l.n) + ' · ' + f1(l.c) + ' CBM</title>' +
        '<rect x="' + x + '" y="' + y0 + '" width="' + len + '" height="' + Hs + '" fill="' + c + '" stroke="#fff"/>' +
        '<polygon points="' + x + ',' + y0 + ' ' + (x + len) + ',' + y0 + ' ' + (x + len + dx) + ',' + (y0 - dy) + ' ' + (x + dx) + ',' + (y0 - dy) + '" fill="' + c + '" opacity=".72" stroke="#fff"/>';
      if (len > 54) g += '<text x="' + (x + len / 2) + '" y="' + (y0 + Hs / 2 - 4) + '" text-anchor="middle" fill="' + ink + '">' + esc(l.n.split(" ")[0]) + '</text><text x="' + (x + len / 2) + '" y="' + (y0 + Hs / 2 + 12) + '" text-anchor="middle" fill="' + ink + '">' + f1(l.c) + '</text>';
      else if (len > 22) g += '<text x="' + (x + len / 2) + '" y="' + (y0 + Hs / 2 + 4) + '" text-anchor="middle" fill="' + ink + '" style="font-size:10px">' + f1(l.c) + '</text>';
      g += '</g>';
      a += len; used += l.c;
    });
    g += '<polygon class="bx-end" points="' + (x0 + Ls) + ',' + y0 + ' ' + (x0 + Ls + dx) + ',' + (y0 - dy) + ' ' + (x0 + Ls + dx) + ',' + (y0 - dy + Hs) + ' ' + (x0 + Ls) + ',' + (y0 + Hs) + '"/>';
    g += '<line class="bx-door" x1="' + (x0 + Ls + dx * .5) + '" x2="' + (x0 + Ls + dx * .5) + '" y1="' + (y0 - dy * .5 + 6) + '" y2="' + (y0 - dy * .5 + Hs - 6) + '"/>';
    if (a < Ls - 1) g += '<rect class="bx-free" x="' + (x0 + a + 2) + '" y="' + (y0 + 3) + '" width="' + Math.max(0, Ls - a - 4) + '" height="' + (Hs - 6) + '"/>' +
      (Ls - a > 110 ? '<text class="bx-freetxt" x="' + (x0 + a + (Ls - a) / 2) + '" y="' + (y0 + Hs / 2 + 4) + '" text-anchor="middle">' + f1(Math.max(0, p.usable - used)) + ' CBM free</text>' : "");
    if (p.be && p.be < p.usable) {
      var bx = x0 + p.be * perCBM;
      g += '<line class="bx-be" x1="' + bx + '" x2="' + bx + '" y1="' + (y0 - 6) + '" y2="' + (y0 + Hs + 6) + '"/><text class="bx-betxt" x="' + bx + '" y="' + (y0 + Hs + (x0 + Ls - bx < 150 ? 34 : 20)) + '" text-anchor="middle">break-even ' + f1(p.be) + '</text>';
    }
    g += '<text class="bx-usetxt" x="' + (x0 + Ls) + '" y="' + (y0 + Hs + 20) + '" text-anchor="end">usable ' + f1(p.usable) + ' CBM</text>';
    svg.setAttribute("viewBox", "0 0 " + W + " " + H);
    svg.innerHTML = g;
  }
  function size(fig) {
    var p;
    try { p = JSON.parse(fig.getAttribute("data-box")); } catch (e) { return; }
    var wrap = fig.closest("[data-fig-wrap]") || fig.parentNode;
    var s = Math.max(16, (wrap.clientWidth - 40 - 2.35 * .42 * 40) / p.maxL / 1.02);
    var h = Math.round(p.H * s + p.W * s * .28 + 56);
    if (fig.style.height !== h + "px") fig.style.height = h + "px";
    draw(fig.querySelector("svg"), p, s);
  }
  function all() { document.querySelectorAll(".fig[data-box]").forEach(size); }
  if (window.ResizeObserver) {
    var ro = new ResizeObserver(function (es) { es.forEach(function (e) { size(e.target); }); });
    document.querySelectorAll(".fig[data-box]").forEach(function (f) { ro.observe(f); });
  } else {
    window.addEventListener("resize", all);
  }
  all();
})();

// Range inputs: show the value next to the label while dragging.
document.addEventListener("input", function (e) {
  if (!e.target.matches('input[type=range]')) return;
  var out = e.target.closest("label") && e.target.closest("label").querySelector("[data-range-out]");
  if (out) out.textContent = e.target.value + "%";
});

// <details data-remember>: stay open across reloads once you've been working in it.
(function () {
  document.querySelectorAll("details[data-remember]").forEach(function (d) {
    var key = "open:" + location.pathname + "#" + d.id;
    try { if (sessionStorage.getItem(key) === "1") d.open = true; } catch (e) {}
    window.addEventListener("pagehide", function () {
      try { sessionStorage.setItem(key, d.open ? "1" : "0"); } catch (e) {}
    });
  });
})();
