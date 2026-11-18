// HyveMynd public dashboard (pinned v2 dashboard, v3 Seerr-style requests).
(function () {
  "use strict";
  var $ = function (id) { return document.getElementById(id); };
  function el(tag, cls, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text != null) e.textContent = text; return e; }
  function svgEl(tag, attrs) { var e = document.createElementNS("http://www.w3.org/2000/svg", tag); for (var k in attrs) e.setAttribute(k, attrs[k]); return e; }
  function api(path, opts) {
    opts = opts || {};
    var h = { "X-HyveMynd-Request": "1" };
    if (opts.body) h["Content-Type"] = "application/json";
    return fetch(path, { method: opts.method || "GET", headers: h, body: opts.body ? JSON.stringify(opts.body) : undefined, credentials: "same-origin" })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (d) { if (!r.ok) { var e = new Error(d.error || d.message || ("HTTP " + r.status)); e.status = r.status; throw e; } return d; }); });
  }
  function mediaColor(kind) { return { tv: "var(--tv)", series: "var(--tv)", episode: "var(--tv)", movie: "var(--movie)", anime: "var(--anime)", music: "var(--music)", track: "var(--music)", album: "var(--music)" }[kind] || "var(--tv)"; }
  function fmtDur(ms) { var m = Math.round(ms / 60000); return m >= 60 ? Math.floor(m / 60) + "h " + (m % 60) + "m" : m + "m"; }
  function fmtSize(b) { if (!b) return "0 B"; var u = ["B", "KB", "MB", "GB", "TB", "PB"], i = 0; while (b >= 1024 && i < 5) { b /= 1024; i++; } return b.toFixed(i > 2 ? 1 : 0) + " " + u[i]; }
  function ago(t) { var s = Math.max(0, (Date.now() - new Date(t)) / 1000); if (s < 90) return "just now"; if (s < 5400) return Math.round(s / 60) + " min ago"; if (s < 172800) return Math.round(s / 3600) + " h ago"; return Math.round(s / 86400) + " days ago"; }
  var ICON = {
    check: '<path d="M5 12l5 5 9-10"/>', clock: '<circle cx="12" cy="12" r="8"/><path d="M12 8v4l3 2"/>', down: '<path d="M12 4v12M6 10l6 6 6-6"/>',
    plus: '<path d="M12 5v14M5 12h14"/>', play: '<path d="M7 4l13 8-13 8z"/>', up: '<path d="M12 19V5M6 11l6-6 6 6"/>',
    straight: '<path d="M5 12h12M13 7l5 5-5 5"/>', swap: '<path d="M4 9h14M14 5l4 4-4 4M20 15H6M10 11l-4 4 4 4"/>'
  };
  function icon(name, size, color) { var s = svgEl("svg", { viewBox: "0 0 24 24", width: size || 16, height: size || 16, fill: "none", stroke: color || "currentColor", "stroke-width": "2.2", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" }); s.innerHTML = ICON[name]; return s; }

  // ------------------------------------------------------------ segmented circle
  var STAGES = ["requested", "found", "download", "transfer", "convert", "ready"];
  var STAGE_NAME = { requested: "Requested", found: "Found", download: "Downloading", transfer: "Transferring", convert: "Converting", ready: "Ready" };
  // The segmented circle, as drawn in the pinned dashboard: six thick arcs
  // (Requested, Found, Download, Transfer, Convert, Ready), finished ones green.
  // Working: the rest of the arc is a marching dashed track with the stage
  // colour filling it, and the percent in the middle. Waiting pulses in the
  // stage colour; stalled blinks yellow with "!"; failed flickers red with "x".
  function arcPath(i, f0, f1) {
    var r = 22, deg = Math.PI / 180, a0 = (i * 60 + 5 + 50 * (f0 || 0)) * deg, a1 = (i * 60 + 5 + 50 * (f1 == null ? 1 : f1)) * deg;
    var p = function (a) { return (30 + r * Math.sin(a)).toFixed(2) + " " + (30 - r * Math.cos(a)).toFixed(2); };
    return "M" + p(a0) + " A" + r + " " + r + " 0 0 1 " + p(a1);
  }
  // Animations keep their phase across redraws (the lists redraw every few
  // seconds), so a pulse or blink never jumps back to its start.
  function phase(node, ms) { node.style.animationDelay = -(Date.now() % ms) + "ms"; return node; }
  function seg(stage, state, progress, size, width) {
    size = size || 30; var w = width || 8;
    var at = STAGES.indexOf(stage), pct = Math.round((progress || 0) * 100);
    var label = state === "failed" ? "failed at step " + (at + 1) + " of 6" : state === "stalled" ? "stalled at step " + (at + 1) + " of 6" : state === "waiting" ? "waiting at step " + (at + 1) + " of 6" : state === "done" ? "done" : "step " + (at + 1) + " of 6" + (pct ? ", " + pct + "% through it" : "");
    var s = svgEl("svg", { viewBox: "0 0 60 60", width: size, height: size, class: "seg", role: "img", "aria-label": label });
    function arc(i, cls, f0, f1) { var d = svgEl("path", { d: arcPath(i, f0, f1), fill: "none", "stroke-width": w }); d.setAttribute("class", cls); s.appendChild(d); return d; }
    var txt = "", tcls = "rt";
    STAGES.forEach(function (st, i) {
      if (i < at || state === "done") { arc(i, "dn"); return; }
      if (i > at) { arc(i, "trk"); return; }
      if (state === "working") {
        phase(arc(i, "trk march"), 900);
        if (pct > 0) { arc(i, "act k-" + st, 0, Math.min(1, pct / 100)); txt = String(pct); }
      } else if (state === "stalled") { phase(arc(i, "stall"), 1100); txt = "!"; tcls = "rt y"; }
      else if (state === "failed") { phase(arc(i, "fail"), 1600); txt = "×"; tcls = "rt r"; }
      else phase(arc(i, "wait k-" + st), 2800);
    });
    var t = svgEl("text", { x: 30, y: 36, "text-anchor": "middle", class: tcls }); t.textContent = txt; s.appendChild(t);
    return s;
  }
  var VERB = { requested: "Requested", found: "Found", download: "Downloading", transfer: "Transferring", convert: "Converting", ready: "Ready" };
  var NOUN = { requested: "Request", found: "Search", download: "Download", transfer: "Transfer", convert: "Convert", ready: "Import" };
  function stageLabel(it) {
    var l;
    if (it.state === "failed") l = NOUN[it.stage] + " failed";
    else if (it.state === "stalled") l = NOUN[it.stage] + " stalled";
    else if (it.state === "done") l = "Imported";
    else if (it.state === "waiting") l = it.stage === "requested" ? "Requested" : VERB[it.stage] + ", in line";
    else l = it.stage === "requested" ? "Approved" : VERB[it.stage];
    if (it.upgrade) l = "Upgrade · " + l.charAt(0).toLowerCase() + l.slice(1);
    return l;
  }
  function stageTone(it) { return it.state === "failed" ? "bad" : it.state === "stalled" ? "warn" : ""; }


  // ------------------------------------------------------------ now playing
  var sessions = [];
  function playing() {
    api("/api/v1/playback").then(function (d) {
      sessions = d.sessions || []; var box = $("playing"); box.replaceChildren();
      if (!sessions.length) { box.appendChild(el("p", "empty", (d.errors && d.errors[0]) || "Nothing is playing.")); return; }
      sessions.forEach(function (x, i) {
        var kind = x.media_type === "episode" ? "tv" : x.media_type === "track" ? "music" : x.media_type === "movie" ? "movie" : "tv";
        var c = el("article", "np"); c.style.borderLeftColor = mediaColor(kind);
        if (x.poster_url) { var im = el("img"); im.src = x.poster_url; im.alt = ""; im.loading = "lazy"; c.appendChild(im); } else c.appendChild(el("span", "ph"));
        var t = el("div", "t");
        var title = x.show_title || x.artist_name || x.media_title;
        t.appendChild(el("div", "t1", title));
        var line = x.show_title ? "S" + x.season_number + "E" + x.episode_number + " · " + x.media_title : x.album_name ? x.media_title + " · " + x.album_name : (x.year || "");
        t.appendChild(el("div", "t2", line));
        t.appendChild(el("div", "t3", (x.username || "") + (x.device || x.player ? " · " + (x.device || x.player) : "")));
        var left = x.duration_ms ? fmtDur(x.progress_ms || 0) + " / " + fmtDur(x.duration_ms) : "";
        if (left) t.appendChild(el("div", "t3", left));
        var b = el("button", "det", "Details"); b.type = "button"; b.onclick = function () { playDetails(x); }; t.appendChild(b);
        c.appendChild(t);
        var frac = x.duration_ms ? Math.min(1, (x.progress_ms || 0) / x.duration_ms) : 0;
        // The pinned dashboard's ring: track, progress in the media colour, a
        // white playhead dot that pulses at the end of the arc, and the play
        // mode in the middle (green straight arrow, orange two-way arrows).
        var pc = svgEl("svg", { viewBox: "0 0 60 60", width: 48, height: 48, class: "pc", role: "img", "aria-label": Math.round(frac * 100) + "% watched, " + (x.is_transcode ? "transcoding" : "direct play") });
        var tt = svgEl("title", {}); tt.textContent = Math.round(frac * 100) + "% watched, " + (x.is_transcode ? "transcoding" : "direct play"); pc.appendChild(tt);
        var R = 22, CC = 2 * Math.PI * R;
        pc.appendChild(svgEl("circle", { cx: 30, cy: 30, r: R, fill: "none", stroke: "#3a3d41", "stroke-width": 6 }));
        pc.appendChild(svgEl("circle", { cx: 30, cy: 30, r: R, fill: "none", stroke: mediaColor(kind), "stroke-width": 6, "stroke-dasharray": (CC * frac).toFixed(1) + " " + CC.toFixed(1), transform: "rotate(-90 30 30)" }));
        var ang = frac * 2 * Math.PI, dot = svgEl("circle", { cx: (30 + R * Math.sin(ang)).toFixed(2), cy: (30 - R * Math.cos(ang)).toFixed(2), r: 3, class: "dot" }); phase(dot, 1800); pc.appendChild(dot);
        var gi = svgEl("g", { transform: "translate(30 30) scale(.86) translate(-12 -12)", fill: "none", stroke: x.is_transcode ? "#f0a35e" : "#3fb97a", "stroke-width": 2.3, "stroke-linecap": "round", "stroke-linejoin": "round" }); gi.innerHTML = x.is_transcode ? ICON.swap : ICON.straight; pc.appendChild(gi);
        c.appendChild(pc); box.appendChild(c);
      });
    }).catch(function () { var box = $("playing"); box.replaceChildren(el("p", "empty", "Now playing is unavailable.")); });
  }
  function playDetails(x) {
    var d = $("playDialog"); d.replaceChildren();
    var h = el("header"); h.appendChild(el("h2", "", x.show_title || x.media_title)); var c = el("button", "x", "Close"); c.type = "button"; c.onclick = function () { d.close(); }; h.appendChild(c); d.appendChild(h);
    function dl(title, rows) { d.appendChild(el("h3", "", title)); var l = el("dl"); rows.forEach(function (r) { if (!r[1]) return; l.appendChild(el("dt", "", r[0])); l.appendChild(el("dd", "", r[1])); }); d.appendChild(l); }
    dl("Playback", [["Mode", x.is_transcode ? "Transcoding" : "Direct play"], ["Video", x.video_decision], ["Audio", x.audio_decision], ["Bitrate", x.bitrate ? (x.bitrate / 1000).toFixed(1) + " Mbps" : ""], ["Position", x.duration_ms ? fmtDur(x.progress_ms || 0) + " of " + fmtDur(x.duration_ms) : ""]]);
    dl("Viewer", [["User", x.username], ["Device", x.device], ["App", x.product || x.player], ["Server", x.server_name], ["Started", x.started_at ? ago(x.started_at) : ""]]);
    d.showModal();
  }

  // ------------------------------------------------------------ on the way
  var expanded = {};
  function bar(seasons) {
    var b = el("span", "bar"); b.setAttribute("role", "img");
    var imp = 0, tot = 0;
    // One pixel per episode in episode order: green in the library, white
    // when a better copy is on the way (upgrade), dim otherwise.
    seasons.forEach(function (s) { imp += s.imported; tot += s.total; var g = el("span", "s"); g.title = "Season " + s.season + ": " + s.imported + " / " + s.total; var px = s.pixels || "";
      for (var i = 0; i < s.total; i++) { var p = el("i"), c = px ? px.charAt(i) : (i < s.imported ? "h" : "."); if (c === "h") p.className = "h"; else if (c === "u") p.className = "u"; g.appendChild(p); } b.appendChild(g); });
    b.setAttribute("aria-label", imp + " of " + tot + " episodes in the library");
    return b;
  }
  function epRow(it) {
    var r = el("div", "ep"); r.appendChild(seg(it.stage, it.state, it.progress, 38, 8));
    r.appendChild(el("span", "code", it.code || ""));
    if (it.upgrade) { var u = el("span", "up"); u.title = "Upgrade of a file already in the library"; u.appendChild(icon("up", 13, "#fff")); r.appendChild(u); }
    r.appendChild(el("span", "stage " + stageTone(it), stageLabel(it))); return r;
  }
  function onway() {
    api("/api/v2/onway").then(function (d) {
      var L = $("laneShows"), R = $("laneOther"); L.replaceChildren(); R.replaceChildren();
      if (!d.shows.length && !d.others.length) { L.appendChild(el("p", "empty", "Nothing is on the way.")); return; }
      d.shows.forEach(function (sh) {
        var c = el("article", "card " + sh.kind); var h = el("div", "card-h"); h.appendChild(el("span", "name", sh.title));
        if (sh.bar && sh.bar.length) { h.appendChild(bar(sh.bar)); h.appendChild(el("span", "cnt", sh.imported + " / " + sh.total)); }
        c.appendChild(h);
        var eps = el("div", "eps"); var key = sh.kind + ":" + sh.title; if (expanded[key]) c.classList.add("open");
        if (!expanded[key]) {
          sh.items.slice(0, 6).forEach(function (it) { eps.appendChild(epRow(it)); });
        } else {
          var groups = {}; sh.items.forEach(function (it) { var k = it.state === "failed" ? "Failed" : it.state === "stalled" ? "Stalled" : STAGE_NAME[it.stage]; (groups[k] = groups[k] || []).push(it); });
          Object.keys(groups).forEach(function (k) { eps.appendChild(el("div", "group-h", k + " · " + groups[k].length)); groups[k].forEach(function (it) { eps.appendChild(epRow(it)); }); });
        }
        c.appendChild(eps);
        var shown = matchMedia("(max-width:860px)").matches ? 4 : 6;
        if (sh.items.length > shown) { var m = el("button", "more", expanded[key] ? "Show less" : "+ " + (sh.items.length - shown) + " more"); m.type = "button"; m.onclick = function () { expanded[key] = !expanded[key]; onway(); }; c.appendChild(m); }
        L.appendChild(c);
      });
      d.others.forEach(function (it) {
        var r = el("div", "slim " + it.kind); r.appendChild(seg(it.stage, it.state, it.progress, 46, 7));
        var t = el("div", "txt"), n = el("div", "nm"); n.appendChild(el("span", "name", it.title + (it.sub && it.kind === "movie" ? " (" + it.sub + ")" : "")));
        if (it.upgrade) { var u = el("span", "up"); u.title = "Upgrade of a file already in the library"; u.appendChild(icon("up", 13, "#fff")); n.appendChild(u); }
        t.appendChild(n); var st = stageLabel(it); if (it.kind === "music" && it.sub) st = it.sub + " · " + st.charAt(0).toLowerCase() + st.slice(1);
        t.appendChild(el("div", "stage " + stageTone(it), st)); r.appendChild(t); R.appendChild(r);
      });
    }).catch(function () { });
  }

  // ------------------------------------------------------------ recently added
  function recent() {
    api("/api/v1/recently-added").then(function (d) {
      var box = $("recent"); box.replaceChildren();
      (d.items || []).slice(0, 6).forEach(function (x) {
        var kind = x.kind === "series" || x.kind === "episode" || x.kind === "show" ? "tv" : x.kind === "movie" ? "movie" : x.kind === "anime" ? "anime" : "music";
        var c = el("div", "ra " + kind);
        if (x.poster_url) { var im = el("img"); im.src = x.poster_url; im.alt = ""; im.loading = "lazy"; c.appendChild(im); } else c.appendChild(el("span", "ph"));
        var t = el("div"); t.appendChild(el("div", "t1", x.show_title || x.title));
        var n = (x.episodes && x.episodes.length) || x.episode_count || 0;
        t.appendChild(el("div", "t2", n ? "+" + n + (n === 1 ? " episode" : " episodes") : (x.year || kind))); c.appendChild(t); box.appendChild(c);
      });
      if (!box.children.length) box.appendChild(el("p", "empty", "Nothing added lately."));
    }).catch(function () { });
  }

  // ------------------------------------------------------------ storage ring
  function libKind(name) { var n = name.toLowerCase(); if (/anime/.test(n)) return "anime"; if (/movie|film/.test(n)) return "movie"; if (/music|audio/.test(n)) return "music"; if (/tv|show|series/.test(n)) return "tv"; return ""; }
  var storage = null;
  function ring() {
    api("/api/v1/storage").then(function (d) {
      storage = d; var s = $("ring"); s.replaceChildren();
      var cats = (d.categories || []).filter(function (c) { return c.bytes; }), used = cats.reduce(function (n, c) { return n + c.bytes; }, 0);
      var total = d.total_bytes || used + (d.free_bytes || 0) || used;
      var R = 25, C = 2 * Math.PI * R, off = 0;
      s.appendChild(svgEl("circle", { cx: 30, cy: 30, r: R, fill: "none", stroke: "#3a3d41", "stroke-width": 5 }));
      cats.forEach(function (c) { var l = total ? C * c.bytes / total : 0, k = libKind(c.name); s.appendChild(svgEl("circle", { cx: 30, cy: 30, r: R, fill: "none", stroke: k ? mediaColor(k) : "#8f959c", "stroke-width": 5, "stroke-dasharray": l.toFixed(2) + " " + C.toFixed(2), "stroke-dashoffset": (-off).toFixed(2), transform: "rotate(-90 30 30)" })); off += l; });
      $("ringBtn").setAttribute("aria-label", "Storage: " + (total ? Math.round(100 * (d.free_bytes || 0) / total) + "% free. " : "") + "Show details");
    }).catch(function () { });
  }
  function storagePop() {
    var p = $("storagePop"); if (!p.hidden) { p.hidden = true; $("ringBtn").setAttribute("aria-expanded", "false"); return; }
    p.replaceChildren(); p.appendChild(el("h3", "", "Storage"));
    if (!storage || !storage.enabled) p.appendChild(el("p", "empty", "Storage is not measured."));
    else {
      var cats = storage.categories || [], used = cats.reduce(function (n, c) { return n + (c.bytes || 0); }, 0), total = storage.total_bytes || used + (storage.free_bytes || 0);
      cats.forEach(function (c) { var r = el("div", "lib"); var sw = el("span", "sw"); sw.style.background = libKind(c.name) ? mediaColor(libKind(c.name)) : "#8f959c"; r.appendChild(sw); r.appendChild(el("span", "n", c.name)); r.appendChild(el("span", "v", c.bytes == null ? "…" : fmtSize(c.bytes) + (total ? " · " + Math.round(100 * c.bytes / total) + "%" : ""))); p.appendChild(r); });
      var f = el("div", "foot"); if (storage.free_bytes != null) f.appendChild(el("span", "", fmtSize(storage.free_bytes) + " free of " + fmtSize(storage.total_bytes)));
      f.appendChild(el("span", "", storage.scanning ? "Scanning now" : "Last scan " + ago(storage.updated_at))); p.appendChild(f);
    }
    p.hidden = false; $("ringBtn").setAttribute("aria-expanded", "true");
  }
  $("ringBtn").onclick = storagePop;

  // ------------------------------------------------------------ donate and access
  api("/api/v1/donations").then(function (d) {
    var links = [["PayPal", d.paypal_url], ["Cash App", d.cashapp_url], [d.custom_label, d.custom_url]].filter(function (x) { return x[0] && x[1]; });
    if (!links.length) return; $("donateBtn").hidden = false;
    $("donateBtn").onclick = function () {
      var p = $("donatePop"); if (!p.hidden) { p.hidden = true; return; }
      p.replaceChildren(); p.appendChild(el("h3", "", d.title || "Support this server"));
      links.forEach(function (x) { var a = el("a", "don", x[0]); a.href = x[1]; a.target = "_blank"; a.rel = "noopener noreferrer"; p.appendChild(a); }); p.hidden = false;
    };
  }).catch(function () { });
  var jellyfin = false;
  api("/api/v1/access/status").then(function (st) {
    if (!st.enabled) return; $("accessBtn").hidden = false; jellyfin = st.provider === "jellyfin";
    if (jellyfin) {
      $("accessHelp").hidden = true; $("accessForm").elements.email.previousSibling.textContent = "Email address";
      $("accessEmailHelp").textContent = "We'll use this address to tell you about your request.";
      $("accessNote").textContent = "Your request is held for approval. If approved, we'll email a link to finish setting up your server account.";
      $("setupHelp").textContent = "Choose a password for your server account below.";
    }
  }).catch(function () { });
  function step(n) { var f = $("accessForm"); f.querySelectorAll("fieldset").forEach(function (s) { s.hidden = s.dataset.step !== String(n); }); $("accessDialog").querySelectorAll(".steps span").forEach(function (s, i) { s.className = i < n ? "on" : ""; }); var i = f.querySelector('fieldset[data-step="' + n + '"] input'); if (i) i.focus(); }
  function valid(n) { return Array.prototype.every.call($("accessForm").querySelectorAll('fieldset[data-step="' + n + '"] input'), function (i) { return i.reportValidity(); }); }
  $("accessBtn").onclick = function () { $("accessForm").hidden = false; $("accessMsg").textContent = ""; step(1); $("accessDialog").showModal(); };
  $("accessForm").querySelector("[data-next]").onclick = function () { if (valid(1)) step(2); };
  $("accessForm").querySelector("[data-back]").onclick = function () { step(1); };
  $("accessForm").onsubmit = function (e) {
    e.preventDefault(); if (!valid(2)) return; var f = e.currentTarget;
    var data = { email: f.elements.email.value.trim(), name: f.elements.name.value.trim(), username: f.elements.username.value.trim() };
    if (!jellyfin) data.connect_username = data.email;
    $("accessMsg").textContent = "Submitting your request…";
    api("/api/v1/access/request", { method: "POST", body: data }).then(function (r) { f.reset(); f.hidden = true; $("accessMsg").textContent = r.message || "Request sent."; }).catch(function (err) { $("accessMsg").textContent = err.message; });
  };
  document.querySelectorAll(".dlg [data-close]").forEach(function (b) { b.onclick = function () { b.closest("dialog").close(); }; });
  var setupToken = "";
  if (location.hash.indexOf("#access-setup=") === 0) {
    setupToken = location.hash.slice(14); history.replaceState(null, "", location.pathname + location.search);
    var ok = /^[a-f0-9]{64}$/.test(setupToken); $("setupForm").hidden = !ok; $("setupMsg").textContent = ok ? "" : "This setup link is invalid. Ask the server owner to resend it."; $("setupDialog").showModal();
  }
  $("setupForm").onsubmit = function (e) {
    e.preventDefault(); var f = e.currentTarget;
    if (f.elements.password.value !== f.elements.confirm.value) { $("setupMsg").textContent = "Passwords do not match."; return; }
    $("setupMsg").textContent = "Finishing setup…";
    api("/api/v1/access/setup", { method: "POST", body: { token: setupToken, password: f.elements.password.value } }).then(function (r) { setupToken = ""; f.hidden = true; $("setupMsg").textContent = r.message || "Done."; }).catch(function (err) { $("setupMsg").textContent = err.message; }).then(function () { f.reset(); });
  };

  // ------------------------------------------------------------ request media, Seerr-style
  var panel = $("reqPanel"), input = $("reqInput"), reqOn = false, admin = false, pending = 0, view = { tab: "discover", detail: null, filter: "all" }, timer = null, cache = {};
  api("/api/v1/media-request/status").then(function (d) { reqOn = !!d.enabled; if (!reqOn) { input.placeholder = "Requests are closed"; } }).catch(function () { });
  api("/api/v1/auth/status").then(function (d) { admin = !!d.admin; if (admin) pendingCount(); }).catch(function () { });
  function pendingCount() {
    api("/api/v1/requests").then(function (d) { pending = (d.requests || []).length; var c = $("reqCount"); c.hidden = !pending; c.textContent = pending; c.setAttribute("aria-label", pending + " requests waiting for approval"); }).catch(function () { });
  }
  function open() { if (!panel.hidden) return; panel.hidden = false; input.setAttribute("aria-expanded", "true"); render(); }
  function close() { panel.hidden = true; input.setAttribute("aria-expanded", "false"); view.detail = null; }
  input.addEventListener("focus", open);
  input.addEventListener("input", function () { clearTimeout(timer); view.detail = null; timer = setTimeout(render, 350); });
  input.addEventListener("keydown", function (e) { if (e.key === "Escape") { close(); input.blur(); } });
  document.addEventListener("click", function (e) {
    if (!e.target.isConnected) return; // a card the panel just redrew
    if (!$("req").contains(e.target)) close();
    if (!$("storagePop").contains(e.target) && e.target.closest && !e.target.closest("#ringBtn")) { $("storagePop").hidden = true; $("ringBtn").setAttribute("aria-expanded", "false"); }
    if (!$("donatePop").contains(e.target) && e.target !== $("donateBtn")) $("donatePop").hidden = true;
  });
  document.addEventListener("keydown", function (e) { if (e.key === "Escape" && !panel.hidden) close(); });

  var STATUS = { 5: ["Available", "var(--ok)", "check"], 4: ["Partly available", "var(--warn)", "check"], 3: ["On the way", "var(--st-download)", "down"], 2: ["Requested", "var(--anime)", "clock"] };
  function kindOf(x, force) { if (force) return force; return x.mediaType === "movie" ? "movie" : "tv"; }
  function card(x, force) {
    var kind = kindOf(x, force), title = x.title || x.name, date = x.releaseDate || x.firstAirDate || "", year = date.slice(0, 4);
    var b = el("button", "sc"); b.type = "button"; b.setAttribute("aria-label", title + (year ? " (" + year + ")" : "") + ", " + (kind === "movie" ? "movie" : kind === "anime" ? "anime" : "series"));
    var p = el("div", "p");
    if (x.poster) { var im = el("img"); im.src = x.poster; im.alt = ""; im.loading = "lazy"; p.appendChild(im); } else p.appendChild(el("span", "ph", title));
    p.appendChild(el("span", "kind " + kind, kind === "movie" ? "MOVIE" : kind === "anime" ? "ANIME" : "SERIES"));
    var st = x.mediaInfo && STATUS[x.mediaInfo.status];
    if (st) { var s = el("span", "stat"); s.style.background = st[1]; s.title = st[0]; s.appendChild(icon(st[2], 14, "#151617")); p.appendChild(s); }
    var hov = el("div", "hov"); if (year) hov.appendChild(el("div", "y", year)); hov.appendChild(el("div", "tt", title)); if (x.overview) hov.appendChild(el("div", "ov", x.overview));
    if (!st || x.mediaInfo.status === 4) { var rq = el("span", "rq"); rq.appendChild(icon("plus", 14, "#fff")); rq.appendChild(document.createTextNode(st ? " Request more" : " Request")); hov.appendChild(rq); }
    p.appendChild(hov); b.appendChild(p);
    b.appendChild(el("div", "nm", title)); b.appendChild(el("div", "yr", year));
    b.onclick = function () { view.detail = { type: x.mediaType === "movie" ? "movie" : "tv", id: x.id, kind: kind, fallback: x }; render(); };
    return b;
  }
  function pendRow() {
    if (!admin || !pending) return null;
    var r = el("div", "rp-pend"); var n = el("span", "req-count", String(pending)); r.appendChild(n);
    r.appendChild(el("span", "", pending + (pending === 1 ? " request waiting" : " requests waiting") + " for approval"));
    var a = el("a", "", "Review"); a.href = "/admin"; r.appendChild(a); return r;
  }
  function list(kind) {
    if (cache[kind] && Date.now() - cache[kind].at < 600000) return Promise.resolve(cache[kind].data);
    return api("/api/v1/media-request/discover?kind=" + encodeURIComponent(kind)).then(function (d) { cache[kind] = { at: Date.now(), data: d.results || [] }; return cache[kind].data; });
  }
  function render() {
    panel.replaceChildren();
    if (!reqOn) { panel.appendChild(el("p", "rp-msg", "Media requests are not open on this server.")); return; }
    if (view.detail) return detail();
    var q = input.value.trim();
    var pr = pendRow(); if (pr) panel.appendChild(pr);
    if (q.length >= 2) return results(q);
    var tabs = el("div", "rp-tabs"); tabs.setAttribute("role", "tablist");
    [["discover", "Discover"], ["movies", "Movies"], ["series", "Series"]].forEach(function (t) {
      var b = el("button", view.tab === t[0] ? "on" : "", t[1]); b.type = "button"; b.setAttribute("role", "tab"); b.setAttribute("aria-selected", view.tab === t[0] ? "true" : "false"); b.onclick = function () { view.tab = t[0]; render(); }; tabs.appendChild(b);
    });
    panel.appendChild(tabs);
    var rows = { discover: [["trending", "Trending"], ["popular-movies", "Popular movies"], ["upcoming", "Upcoming"]], movies: [["popular-movies", "Popular movies"], ["upcoming", "Upcoming"], ["trending-movies", "Trending movies"]], series: [["popular-tv", "Popular series"], ["trending-tv", "Trending series"]] }[view.tab] || [];
    rows.forEach(function (rw) {
      var sec = el("section", "rp-row"); sec.appendChild(el("h3", "", rw[1])); var strip = el("div", "rp-strip"); strip.appendChild(el("span", "empty", "Loading…")); var sw = el("div", "rp-sw"); sw.appendChild(strip);
      [["l", -1, "Scroll left"], ["r", 1, "Scroll right"]].forEach(function (a) { var bt = el("button", "rp-arrow " + a[0]); bt.type = "button"; bt.setAttribute("aria-label", a[2]); bt.textContent = a[1] < 0 ? "\u2039" : "\u203a"; bt.onclick = function (e) { e.stopPropagation(); strip.scrollBy({ left: a[1] * strip.clientWidth * 0.85, behavior: "smooth" }); }; sw.appendChild(bt); });
      sec.appendChild(sw); panel.appendChild(sec);
      list(rw[0]).then(function (items) { strip.replaceChildren(); items.forEach(function (x) { strip.appendChild(card(x)); }); if (!items.length) sec.remove(); }).catch(function () { sec.remove(); });
    });
  }
  function results(q) {
    var head = el("div", "rp-head"); head.appendChild(el("span", "", "Results for")); head.appendChild(el("span", "q", "“" + q + "”"));
    var f = el("div", "rp-filter"); [["all", "All"], ["movie", "Movies"], ["tv", "Series"]].forEach(function (o) { var b = el("button", view.filter === o[0] ? "on" : "", o[1]); b.type = "button"; b.onclick = function () { view.filter = o[0]; render(); }; f.appendChild(b); }); head.appendChild(f);
    panel.appendChild(head); var grid = el("div", "rp-grid"); grid.appendChild(el("span", "empty", "Searching…")); panel.appendChild(grid);
    api("/api/v1/media-request/search?query=" + encodeURIComponent(q)).then(function (d) {
      if (input.value.trim() !== q) return; grid.replaceChildren();
      var items = (d.results || []).filter(function (x) { return view.filter === "all" || x.mediaType === view.filter; });
      if (!items.length) grid.appendChild(el("p", "rp-msg", "Nothing found.")); items.forEach(function (x) { grid.appendChild(card(x)); });
    }).catch(function (e) { grid.replaceChildren(el("p", "rp-msg", e.message)); });
  }
  function detail() {
    var dv = view.detail; panel.appendChild(el("p", "rp-msg", "Loading…"));
    api("/api/v1/media-request/title?type=" + dv.type + "&id=" + dv.id).then(function (t) { panel.replaceChildren(); titlePage(t); }).catch(function () { panel.replaceChildren(); simplePage(dv.fallback, dv.type); });
  }
  function backBtn() { var b = el("button", "td-backbtn", "← Back"); b.type = "button"; b.onclick = function () { view.detail = null; render(); }; return b; }
  function pill(text, color, fill) { var p = el("span", "pill" + (fill ? " fill" : ""), text); p.style.color = color; if (fill) p.style.background = color; return p; }
  function titlePage(t) {
    var top = el("div", "td-back"); if (t.backdrop) { var bg = el("div", "td-bg"); bg.style.backgroundImage = 'url("' + t.backdrop + '")'; top.appendChild(bg); } top.appendChild(el("div", "td-shade")); top.appendChild(backBtn());
    var row = el("div", "td-row"); var p = el("div", "p"); if (t.poster) { var im = el("img"); im.src = t.poster; im.alt = ""; p.appendChild(im); } row.appendChild(p);
    var col = el("div", "td-col"); var pl = el("div", "pills"); pl.appendChild(pill(t.kind === "movie" ? "MOVIE" : t.kind === "anime" ? "ANIME" : "SERIES", mediaColor(t.kind), true));
    var st = STATUS[t.status]; if (st) pl.appendChild(pill(st[0], st[1])); col.appendChild(pl);
    var h = el("h2", "", t.title + " "); if (t.year) h.appendChild(el("span", "", "(" + t.year + ")")); col.appendChild(h);
    col.appendChild(el("div", "td-meta", [(t.genres || []).join(", "), t.runtime ? t.runtime + " min" : "", t.network, t.kind !== "movie" && t.seasons ? t.seasons.length + (t.seasons.length === 1 ? " season" : " seasons") : "", t.state].filter(Boolean).join(" · ")));
    row.appendChild(col);
    var acts = el("div", "td-acts"); var msg = el("p", "rp-msg");
    if (t.kind === "movie") {
      var rb = el("button", "btn primary"); rb.type = "button"; rb.appendChild(icon("plus", 15, "#fff")); rb.appendChild(document.createTextNode(st ? " " + st[0] : " Request")); rb.disabled = !!st;
      rb.onclick = function () { send({ media_type: "movie", media_id: t.id }, rb, msg); }; acts.appendChild(rb);
    }
    if (t.trailer) { var tr = el("a", "btn"); tr.href = "https://www.youtube.com/watch?v=" + encodeURIComponent(t.trailer); tr.target = "_blank"; tr.rel = "noopener noreferrer"; tr.appendChild(icon("play", 14)); tr.appendChild(document.createTextNode(" Trailer")); acts.appendChild(tr); }
    row.appendChild(acts); top.appendChild(row); panel.appendChild(top);
    var body = el("div", "td-body"); var left = el("div"); left.appendChild(el("h3", "", "Overview")); left.appendChild(el("p", "", t.overview || "No overview."));
    if (t.cast && t.cast.length) { left.appendChild(el("h3", "", "Cast")).style.marginTop = "14px"; var cs = el("div", "cast"); t.cast.forEach(function (c) { var d = el("div"); if (c.photo) { var i = el("img"); i.src = c.photo; i.alt = ""; i.loading = "lazy"; d.appendChild(i); } else d.appendChild(el("span", "ph")); d.appendChild(el("span", "", c.name)); cs.appendChild(d); }); left.appendChild(cs); }
    body.appendChild(left);
    if (t.kind !== "movie") body.appendChild(picker(t, msg)); else { var side = el("div"); side.appendChild(msg); body.appendChild(side); }
    panel.appendChild(body);
  }
  var SEASON = { available: ["Available", "var(--ok)"], partial: ["Partly available", "var(--warn)"], onTheWay: ["On the way", "var(--st-download)"], requested: ["Requested", "var(--anime)"], unreleased: ["Not released", "var(--st-requested)"] };
  function picker(t, msg) {
    var box = el("div", "picker"); var h = el("div", "h"); h.appendChild(document.createTextNode("Request seasons")); var al = el("label"); var all = el("input"); all.type = "checkbox"; al.appendChild(all); al.appendChild(document.createTextNode(" all seasons")); h.appendChild(al); box.appendChild(h);
    var boxes = [];
    (t.seasons || []).forEach(function (s) {
      var lock = s.status === "available" || s.status === "onTheWay" || s.status === "requested";
      var r = el("label", "s"); var cb = el("input"); cb.type = "checkbox"; cb.disabled = lock; cb.checked = lock; cb.dataset.n = s.number; if (!lock) boxes.push(cb); cb.onchange = upd; r.appendChild(cb);
      r.appendChild(el("b", "", "Season " + s.number)); r.appendChild(el("span", "e", s.episodes ? s.episodes + " episodes" : "announced"));
      var ss = SEASON[s.status]; if (ss) r.appendChild(pill(ss[0], ss[1])); box.appendChild(r);
    });
    var f = el("div", "f"); var go = el("button", "btn primary"); go.type = "button"; f.appendChild(go); box.appendChild(f); box.appendChild(msg);
    function chosen() { return boxes.filter(function (b) { return b.checked; }).map(function (b) { return +b.dataset.n; }); }
    function upd() { var c = chosen(); all.checked = boxes.length && c.length === boxes.length; go.disabled = !c.length; go.textContent = !boxes.length ? "Nothing more to request" : !c.length ? "Pick seasons" : c.length === 1 ? "Request season " + c[0] : "Request " + c.length + " seasons"; }
    all.onchange = function () { boxes.forEach(function (b) { b.checked = all.checked; }); upd(); };
    all.disabled = !boxes.length; upd();
    go.onclick = function () { send({ media_type: "tv", media_id: t.id, seasons: chosen() }, go, msg); };
    return box;
  }
  function simplePage(x, type) {
    // Without TMDB details (requests kept by Seerr): ask for the whole title.
    panel.appendChild(backBtn()); var title = x.title || x.name;
    var b = el("div", "td-body"); var left = el("div"); left.appendChild(el("h3", "", title)); left.appendChild(el("p", "", x.overview || "")); b.appendChild(left);
    var side = el("div"); var msg = el("p", "rp-msg"); var go = el("button", "btn primary", type === "tv" ? "Request all seasons" : "Request"); go.type = "button";
    go.onclick = function () { send({ media_type: type, media_id: x.id, all_seasons: type === "tv" }, go, msg); }; side.appendChild(go); side.appendChild(msg); b.appendChild(side); panel.appendChild(b);
  }
  function send(body, btn, msg) {
    btn.disabled = true; msg.textContent = "Sending…";
    api("/api/v1/media-request", { method: "POST", body: body }).then(function () { msg.textContent = "Requested. It waits for the admin's approval."; btn.textContent = "Requested"; cache = {}; if (admin) pendingCount(); onway(); })
      .catch(function (e) { btn.disabled = false; msg.textContent = e.message; });
  }

  // ------------------------------------------------------------ refresh
  function tick() { if (document.hidden) return; playing(); onway(); }
  playing(); onway(); recent(); ring();
  setInterval(tick, 10000); setInterval(recent, 120000); setInterval(ring, 300000);
  setInterval(function () { if (admin) pendingCount(); }, 60000);
})();
