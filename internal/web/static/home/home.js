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
  function mediaColor(kind) { return { tv: "var(--tv)", series: "var(--tv)", episode: "var(--tv)", movie: "var(--movie)", anime: "var(--anime)", music: "var(--music)", track: "var(--music)", album: "var(--music)", book: "var(--book)" }[kind] || "var(--tv)"; }
  function fmtDur(ms) { var m = Math.round(ms / 60000); return m >= 60 ? Math.floor(m / 60) + "h " + (m % 60) + "m" : m + "m"; }
  function fmtSize(b) { if (!b) return "0 B"; var u = ["B", "KB", "MB", "GB", "TB", "PB"], i = 0; while (b >= 1024 && i < 5) { b /= 1024; i++; } return b.toFixed(i > 2 ? 1 : 0) + " " + u[i]; }
  function ago(t) { var s = Math.max(0, (Date.now() - new Date(t)) / 1000); if (s < 90) return "just now"; if (s < 5400) return Math.round(s / 60) + " min ago"; if (s < 172800) return Math.round(s / 3600) + " h ago"; return Math.round(s / 86400) + " days ago"; }
  var ICON = {
    check: '<path d="M5 12l5 5 9-10"/>', clock: '<circle cx="12" cy="12" r="8"/><path d="M12 8v4l3 2"/>', down: '<path d="M12 4v12M6 10l6 6 6-6"/>',
    plus: '<path d="M12 5v14M5 12h14"/>', play: '<path d="M7 4l13 8-13 8z"/>', up: '<path d="M12 19V5M6 11l6-6 6 6"/>',
    x: '<path d="M6 6l12 12M18 6L6 18"/>', straight: '<path d="M5 12h12M13 7l5 5-5 5"/>', swap: '<path d="M4 9h14M14 5l4 4-4 4M20 15H6M10 11l-4 4 4 4"/>'
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
  // An arc of a circle from deg0 to deg1 (clockwise from the top).
  function ringArc(cx, cy, r, deg0, deg1) {
    var d = Math.PI / 180, p = function (a) { return (cx + r * Math.sin(a * d)).toFixed(2) + " " + (cy - r * Math.cos(a * d)).toFixed(2); };
    return "M" + p(deg0) + " A" + r + " " + r + " 0 " + (deg1 - deg0 > 180 ? 1 : 0) + " 1 " + p(deg1);
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
      if (state === "working" || state === "waiting") {
        // Only the current section moves: tinted in its stage colour with
        // thin dashes flowing along it (slower while waiting in line).
        arc(i, "trk"); arc(i, "cur k-" + st);
        var fl = arc(i, "flow k-" + st + (state === "waiting" ? " slow" : "")); fl.setAttribute("stroke-width", 2.6);
        phase(fl, state === "waiting" ? 2400 : 1200);
        if (state === "working") {
          // The section's own progress: a thin ring on the inner edge.
          s.appendChild(svgEl("circle", { cx: 30, cy: 30, r: 15, fill: "none", class: "in-trk", "stroke-width": 2.2 }));
          if (pct > 0) { var ip = svgEl("path", { d: ringArc(30, 30, 15, 0, Math.min(359.9, pct * 3.6)), fill: "none", "stroke-width": 2.2, "stroke-linecap": "round" }); ip.setAttribute("class", "in k-" + st); s.appendChild(ip); txt = String(pct); }
        }
      } else if (state === "stalled") { phase(arc(i, "stall"), 1100); txt = "!"; tcls = "rt y"; }
      else if (state === "failed") { phase(arc(i, "fail"), 1600); txt = "×"; tcls = "rt r"; }
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
        // The pinned dashboard's ring: track, progress in the media colour,
        // a flowing dashed line round the rest, and the play mode in the
        // middle (green straight arrow, orange two-way arrows).
        var pc = svgEl("svg", { viewBox: "0 0 60 60", width: 48, height: 48, class: "pc", role: "img", "aria-label": Math.round(frac * 100) + "% watched, " + (x.is_transcode ? "transcoding" : "direct play") });
        var tt = svgEl("title", {}); tt.textContent = Math.round(frac * 100) + "% watched, " + (x.is_transcode ? "transcoding" : "direct play"); pc.appendChild(tt);
        var R = 22, CC = 2 * Math.PI * R;
        pc.appendChild(svgEl("circle", { cx: 30, cy: 30, r: R, fill: "none", stroke: "#3a3d41", "stroke-width": 6 }));
        pc.appendChild(svgEl("circle", { cx: 30, cy: 30, r: R, fill: "none", stroke: mediaColor(kind), "stroke-width": 6, "stroke-dasharray": (CC * frac).toFixed(1) + " " + CC.toFixed(1), transform: "rotate(-90 30 30)" }));
        // The old wire's flow through the part still to watch, and a white
        // playhead at the edge of what has been watched.
        if (frac < 0.995) {
          var fl = svgEl("path", { d: ringArc(30, 30, R, Math.max(frac, 0.003) * 360, 359.9), fill: "none", stroke: mediaColor(kind), "stroke-width": 2.6, class: "flow" });
          phase(fl, 1200); pc.appendChild(fl);
        }
        var ang = frac * 2 * Math.PI; pc.appendChild(svgEl("circle", { cx: (30 + R * Math.sin(ang)).toFixed(2), cy: (30 - R * Math.cos(ang)).toFixed(2), r: 3.2, class: "head" }));
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
  // The same as a ring: one slice per episode in episode order, a small gap
  // between seasons, the share in the library written in the middle.
  // ringStats counts a show's episodes by state: in the library, upgraded
  // (at the profile's cutoff), not upgraded yet, upgrading now.
  function ringStats(seasons) {
    var r = { tot: 0, imp: 0, done: 0, old: 0, up: 0 };
    seasons.forEach(function (x) { r.tot += x.total; r.imp += x.imported; var px = x.pixels || "";
      for (var i = 0; i < px.length; i++) { var c = px.charAt(i); if (c === "h") r.done++; else if (c === "o") r.old++; else if (c === "u") r.up++; } });
    r.upgrading = r.old + r.up > 0;
    r.pctUp = r.done + r.old + r.up ? Math.floor(r.done * 100 / (r.done + r.old + r.up)) : 0;
    return r;
  }
  // fillFor gives how far an episode on the way has come (0..1) from the
  // show's items, by its SxxEyy code.
  function fillFor(items) {
    var m = {};
    (items || []).forEach(function (it) {
      var mm = /^S(\d+)E(\d+)(?:-?E?(\d+))?/.exec(it.code || ""); if (!mm) return;
      var at = STAGES.indexOf(it.stage), f = it.state === "done" ? 1 : at < 0 ? 0 : (at + Math.min(1, Math.max(0, it.progress || 0))) / STAGES.length;
      for (var e = +mm[2]; e <= +(mm[3] || mm[2]) && e < +mm[2] + 50; e++) m[+mm[1] + ":" + e] = f; // S01E05-E06 covers both
    });
    return function (season, ep) { var f = m[season + ":" + ep]; return f == null ? 0 : f; };
  }
  function epRing(seasons, size, items) {
    var st = ringStats(seasons), tot = st.tot, imp = st.imp, fill = fillFor(items);
    var svg = svgEl("svg", { viewBox: "0 0 60 60", width: size || 40, height: size || 40, class: "epring", role: "img" });
    if (!tot) return svg;
    var R = 24, C = 30, n = seasons.length;
    var sgap = n > 1 ? Math.min(8, 90 / n) : 0;          // degrees between seasons
    var egap = tot <= 60 ? Math.min(1.6, 120 / tot) : 0;  // between episodes, when there are few
    var per = (360 - sgap * (n > 1 ? n : 0)) / tot;
    function pt(a) { var r = (a - 90) * Math.PI / 180; return (C + R * Math.cos(r)).toFixed(2) + " " + (C + R * Math.sin(r)).toFixed(2); }
    function arc(g, a0, a1, cls) {
      if (a1 - a0 <= 0.05) return;
      var d = a1 - a0 >= 359.9 ? "M " + pt(0) + " A " + R + " " + R + " 0 1 1 " + pt(180) + " A " + R + " " + R + " 0 1 1 " + pt(359.99)
        : "M " + pt(a0) + " A " + R + " " + R + " 0 " + (a1 - a0 > 180 ? 1 : 0) + " 1 " + pt(a1);
      g.appendChild(svgEl("path", { d: d, class: cls, fill: "none", "stroke-width": 8 }));
    }
    var a = sgap / 2;
    seasons.forEach(function (x) {
      var g = svgEl("g", {}); var t = svgEl("title", {}); t.textContent = "Season " + x.season + ": " + x.imported + " / " + x.total; g.appendChild(t);
      var px = x.pixels || "";
      function cls(i) { var c = px ? px.charAt(i) : (i < x.imported ? "h" : "."); return c === "h" ? "h" : c === "o" ? "o" : c === "u" ? "u" : "m"; }
      // An episode on the way fills as it moves through the steps: white over
      // its old file for an upgrade, green over grey for a new episode.
      var mv = 0;
      function moving(i, a0, a1) {
        var c = px.charAt(i), num = (x.moving || [])[mv++], f = Math.max(0.12, fill(x.season, num));
        arc(g, a0, a1, c === "u" ? "o" : "m");
        arc(g, a0, a0 + (a1 - a0) * Math.min(1, f), c === "u" ? "u" : "n");
      }
      function isMoving(i) { var c = px.charAt(i); return c === "u" || c === "w"; }
      if (egap) {
        for (var i = 0; i < x.total; i++) {
          var a0 = a + i * per + egap / 2, a1 = a + (i + 1) * per - egap / 2;
          if (isMoving(i)) moving(i, a0, a1); else arc(g, a0, a1, cls(i));
        }
      } else {
        var start = 0, cur = null;
        for (var k = 0; k <= x.total; k++) {
          var c = k < x.total ? (isMoving(k) ? "*" : cls(k)) : null;
          if (c !== cur) { if (cur && cur !== "*") arc(g, a + start * per, a + k * per, cur); start = k; cur = c; }
          if (c === "*") { moving(k, a + k * per, a + (k + 1) * per); cur = null; start = k + 1; }
        }
      }
      a += x.total * per + (n > 1 ? sgap : 0);
      svg.appendChild(g);
    });
    // Being upgraded: the middle shows how much is upgraded; otherwise how
    // much is in the library.
    var pct = st.upgrading ? st.pctUp : Math.floor(imp * 100 / tot);
    var tx = svgEl("text", { x: 30, y: 30, class: "rt", "text-anchor": "middle", "dominant-baseline": "central" }); tx.textContent = pct + "%"; svg.appendChild(tx);
    svg.setAttribute("aria-label", imp + " of " + tot + " episodes in the library" + (st.upgrading ? ", " + st.done + " upgraded, " + st.up + " upgrading now" : ""));
    return svg;
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
        var c = el("article", "card " + sh.kind); var h = el("div", "card-h"); var nm = el("div", "nm"); var tn = el("span", "name", sh.title); tn.title = sh.title; nm.appendChild(tn); h.appendChild(nm);
        if (sh.bar && sh.bar.length) { var rs = ringStats(sh.bar);
          nm.appendChild(el("span", "cnt", rs.upgrading ? rs.done + " / " + (rs.done + rs.old + rs.up) + " upgraded" + (rs.up ? " · " + rs.up + " now" : "") : sh.imported + " / " + sh.total + " episodes"));
          h.appendChild(epRing(sh.bar, 40, sh.items)); }
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
  function libKind(name) { var n = name.toLowerCase(); if (/anime/.test(n)) return "anime"; if (/movie|film/.test(n)) return "movie"; if (/book/.test(n)) return "book"; if (/music|audio/.test(n)) return "music"; if (/tv|show|series/.test(n)) return "tv"; return ""; }
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
  // The storage panel (pinned v5): trends on the left, the library on the right.
  var KINDS = ["tv", "anime", "movie", "music", "book"], DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  function stackBars(days, val) {
    var W = 358, H = 52, gap = 5, n = days.length, bw = (W - gap * (n - 1)) / n;
    var mx = Math.max.apply(null, days.map(function (d) { return KINDS.reduce(function (t, k) { return t + (val(d)[k] || 0); }, 0); })) || 1;
    var svg = svgEl("svg", { viewBox: "0 0 " + W + " " + H, width: "100%", height: H, role: "img" });
    days.forEach(function (d, i) {
      var x = i * (bw + gap), y = H - 16;
      KINDS.forEach(function (k) { var v = val(d)[k] || 0; if (!v) return; var h = v / mx * (H - 22); y -= h; svg.appendChild(svgEl("rect", { x: x.toFixed(1), y: y.toFixed(1), width: bw.toFixed(1), height: h.toFixed(1), rx: 1.5, fill: mediaColor(k) })); });
      var t = svgEl("text", { x: (x + bw / 2).toFixed(1), y: H - 3, "text-anchor": "middle", "font-size": 11, fill: "#a0a4a8" }); t.textContent = DAYS[new Date(d.date + "T12:00:00").getDay()]; svg.appendChild(t);
    });
    return svg;
  }
  function insCard(label, value) { var c = el("div", "ins-card"); var h = el("div", "ins-ch"); h.appendChild(el("span", "", label)); if (value != null) h.appendChild(el("b", "mono", value)); c.appendChild(h); return c; }
  function splitBar(parts, label) {
    var w = el("div"); var bar = el("div", "ins-split"); bar.setAttribute("role", "img"); bar.setAttribute("aria-label", label + ": " + parts.map(function (p) { return p[0] + " " + p[1] + "%"; }).join(", "));
    parts.forEach(function (p) { var s = el("span"); s.style.width = p[1] + "%"; s.style.background = p[2]; bar.appendChild(s); }); w.appendChild(bar);
    var leg = el("div", "ins-leg"); parts.forEach(function (p) { var i = el("span"); var sw = el("i"); sw.style.background = p[2]; i.appendChild(sw); i.appendChild(document.createTextNode(p[0] + " ")); i.appendChild(el("b", "mono", p[1] + "%")); leg.appendChild(i); }); w.appendChild(leg);
    return w;
  }
  function drawTrends(box, d) {
    box.replaceChildren(); var h = el("div", "ins-h"); h.appendChild(el("h3", "", "Trends")); h.appendChild(el("span", "ins-note", "last " + d.days.length + " days")); box.appendChild(h);
    var added = d.days.reduce(function (t, x) { return t + KINDS.reduce(function (a, k) { return a + (x.added[k] || 0); }, 0); }, 0);
    var c1 = insCard("ADDED", String(added)); var s1 = stackBars(d.days, function (x) { return x.added; }); s1.setAttribute("aria-label", "Added per day, by library"); c1.appendChild(s1); box.appendChild(c1);
    var hours = d.days.reduce(function (t, x) { return t + KINDS.reduce(function (a, k) { return a + (x.watchHours[k] || 0); }, 0); }, 0);
    var c2 = insCard("WATCHED", d.watchKnown ? (hours < 10 ? hours.toFixed(1) : Math.round(hours)) + " h" : null);
    if (!d.watchKnown) c2.appendChild(el("p", "ins-empty", "Tracearr is not set up."));
    else {
      var s2 = stackBars(d.days, function (x) { return x.watchHours; }); s2.setAttribute("aria-label", "Hours watched per day, by library"); c2.appendChild(s2);
      if (d.play.direct + d.play.transcode > 0) { var dp = Math.round(d.play.direct * 100); c2.appendChild(el("div", "ins-sub", "How it plays")); c2.appendChild(splitBar([["Direct play", dp, "var(--ok)"], ["Transcoded", 100 - dp, "var(--st-convert)"]], "How it plays")); }
    }
    box.appendChild(c2);
    var t = d.tdarr; var c3 = insCard("SPACE SAVED BY TDARR", null); c3.querySelector(".ins-ch").appendChild(el("span", "ins-note", "all time"));
    if (!t || !t.files) c3.appendChild(el("p", "ins-empty", "Nothing converted yet."));
    else {
      var kept = t.after, saved = Math.max(0, t.before - t.after), kp = t.before ? Math.max(1, Math.round(kept / t.before * 100)) : 0;
      var bar = el("div", "ins-tdarr"); bar.setAttribute("role", "img"); bar.setAttribute("aria-label", "Of " + fmtSize(t.before) + " converted, " + fmtSize(kept) + " kept and " + fmtSize(saved) + " saved");
      var a = el("span", "kept"); a.style.width = kp + "%"; bar.appendChild(a); bar.appendChild(el("span", "saved")); c3.appendChild(bar);
      var lg = el("div", "ins-leg spread"); var l1 = el("span"); var i1 = el("i"); i1.style.background = "var(--st-convert)"; l1.appendChild(i1); l1.appendChild(document.createTextNode("Kept ")); l1.appendChild(el("b", "mono", fmtSize(kept)));
      var l2 = el("span"); var i2 = el("i"); i2.style.background = "#3a7a45"; l2.appendChild(i2); l2.appendChild(document.createTextNode("Saved ")); l2.appendChild(el("b", "mono good", fmtSize(saved))); lg.appendChild(l1); lg.appendChild(l2); c3.appendChild(lg);
      c3.appendChild(el("p", "ins-foot", fmtSize(t.before) + " went into Tdarr across " + t.files + " file" + (t.files === 1 ? "" : "s")));
    }
    box.appendChild(c3);
  }
  function countCell(v, unit, shade, kind) { var c = el("span", "ins-c"); var n = el("b", "mono", v.toLocaleString()); n.style.color = shade ? "color-mix(in srgb, " + mediaColor(kind) + " " + (100 - shade) + "%, #fff)" : mediaColor(kind); c.appendChild(n); c.appendChild(document.createTextNode(" " + unit)); return c; }
  function countsRow(kind, c) {
    if (!c) return null; var r = el("div", "ins-counts");
    if (kind === "movie") { if (!c.movies) return null; r.appendChild(el("span")); r.appendChild(countCell(c.movies, "movies", 0, kind)); r.appendChild(el("span")); return r; }
    if (kind === "book") { if (!c.ebooks && !c.audiobooks) return null; r.className = "ins-counts six"; var a = countCell(c.ebooks || 0, "ebooks", 0, kind), b = countCell(c.audiobooks || 0, "audiobooks", 0, kind); a.className += " l"; b.className += " r"; r.appendChild(a); r.appendChild(b); return r; }
    var cells = kind === "music" ? [[c.artists, "artists"], [c.albums, "albums"], [c.tracks, "tracks"]] : [[c.shows, "shows"], [c.seasons, "seasons"], [c.episodes, "episodes"]];
    if (!cells.some(function (x) { return x[0]; })) return null;
    cells.forEach(function (x, i) { r.appendChild(countCell(x[0] || 0, x[1], [0, 38, 68][i], kind)); }); return r;
  }
  function drawLibrary(box, d) {
    box.replaceChildren(); var h = el("div", "ins-h"); h.appendChild(el("h3", "", "Library"));
    if (storage && storage.free_bytes != null && storage.total_bytes) { var f = el("span", "ins-note"); f.appendChild(el("b", "mono", fmtSize(storage.free_bytes))); f.appendChild(document.createTextNode(" free of " + fmtSize(storage.total_bytes))); h.appendChild(f); }
    box.appendChild(h);
    if (!storage || !storage.enabled) { box.appendChild(el("p", "ins-empty", "Storage is not measured.")); return; }
    var cats = storage.categories || [], used = cats.reduce(function (n, c) { return n + (c.bytes || 0); }, 0), total = storage.total_bytes || used + (storage.free_bytes || 0), seen = {};
    cats.forEach(function (c) {
      var k = libKind(c.name), b = el("div", "ins-lib"), top = el("div", "lib"); var sw = el("span", "sw"); sw.style.background = k ? mediaColor(k) : "#8f959c"; top.appendChild(sw); top.appendChild(el("span", "n", c.name));
      top.appendChild(el("span", "v", c.bytes == null ? "…" : fmtSize(c.bytes) + (total ? " · " + Math.round(100 * c.bytes / total) + "%" : ""))); b.appendChild(top);
      if (d && k) { var byName = (k === "book" && d.booksFromBookOrbit) ? null : (d.libraries || {})[c.name.toLowerCase()], cnt = byName || (!seen[k] ? (d.library || {})[k] : null); seen[k] = true; var r = countsRow(k, cnt); if (r) b.appendChild(r); }
      box.appendChild(b);
    });
    if (d) drawBottom(box, d);
  }
  function drawBottom(box, d) {
    var picks = d.picks || [], u = d.upgrades || {};
    if (!picks.length && !u.episodes && !u.movies && !(u.share >= 0)) return;
    var row = el("div", "ins-bottom");
    if (picks.length) {
      var pk = el("div", "ins-pick"), i = 0; var ph = el("div", "ins-ph"); ph.appendChild(el("span", "", "TONIGHT"));
      var sh = el("button", "ins-shuf"); sh.type = "button"; sh.setAttribute("aria-label", "Pick something else"); sh.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M16 3h5v5M4 20L21 3M21 16v5h-5M15 15l6 6M4 4l5 5"/></svg>';
      ph.appendChild(sh); pk.appendChild(ph); var body = el("div", "ins-pbody"); pk.appendChild(body);
      var show = function () { var x = picks[i % picks.length]; body.replaceChildren(); var art = x.poster ? el("img", "ins-poster") : el("div", "ins-poster"); if (x.poster) { art.src = x.poster; art.alt = ""; art.loading = "lazy"; } else art.style.background = "linear-gradient(160deg,color-mix(in srgb," + mediaColor(x.kind) + " 35%,transparent),#303336)"; body.appendChild(art);
        var tx = el("div", "ins-ptx"); tx.appendChild(el("div", "ins-pt", x.title)); tx.appendChild(el("div", "ins-pm", String(x.year || ""))); tx.appendChild(el("div", "ins-pm", x.unwatched ? "unwatched" : (x.kind === "tv" ? "show" : "movie"))); body.appendChild(tx); };
      sh.onclick = function (e) { e.stopPropagation(); i++; show(); }; show(); if (picks.length < 2) sh.hidden = true; row.appendChild(pk);
    }
    var up = el("div", "ins-up"); var uh = el("div", "ins-h small"); uh.appendChild(el("h4", "", "Upgrades")); uh.appendChild(el("span", "ins-note", "last " + d.days.length + " days")); up.appendChild(uh);
    var cr = el("div", "ins-counts two"); var e1 = el("span", "ins-c"); e1.appendChild(el("b", "mono good", String(u.episodes || 0))); e1.appendChild(document.createTextNode(" episodes")); var e2 = el("span", "ins-c"); e2.appendChild(el("b", "mono good", String(u.movies || 0))); e2.appendChild(document.createTextNode(" movies")); cr.appendChild(e1); cr.appendChild(e2); up.appendChild(cr);
    if (u.share >= 0) { var pc = Math.round(u.share * 100); up.appendChild(el("div", "ins-sub", "Library at the better quality")); var q = el("div", "ins-split q"); q.setAttribute("role", "img"); q.setAttribute("aria-label", pc + "% upgraded, " + (100 - pc) + "% not yet"); var g = el("span"); g.style.width = pc + "%"; g.style.background = "#7ee08a"; q.appendChild(g); var rest = el("span"); rest.style.flex = "1"; rest.style.background = "var(--line)"; q.appendChild(rest); up.appendChild(q);
      var lg = el("div", "ins-leg spread"); var a = el("span"); a.appendChild(el("b", "mono good", pc + "%")); a.appendChild(document.createTextNode(" upgraded")); var b = el("span"); b.appendChild(el("b", "mono", (100 - pc) + "%")); b.appendChild(document.createTextNode(" not yet")); lg.appendChild(a); lg.appendChild(b); up.appendChild(lg); }
    row.appendChild(up); box.appendChild(row);
  }
  function storagePop() {
    var p = $("storagePop"); if (!p.hidden) { p.hidden = true; $("ringBtn").setAttribute("aria-expanded", "false"); return; }
    p.replaceChildren(); p.classList.add("ins");
    var left = el("section", "ins-left"), right = el("section", "ins-right"); left.setAttribute("aria-label", "Trends"); right.setAttribute("aria-label", "Library");
    left.appendChild(el("p", "ins-empty", "Loading…")); p.appendChild(left); p.appendChild(right); drawLibrary(right, null);
    api("/api/v1/insights").then(function (d) { drawTrends(left, d); drawLibrary(right, d); }).catch(function () { left.replaceChildren(el("p", "ins-empty", "Trends are unavailable right now.")); });
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
      $("accessNote").textContent = "At least 8 characters. Once you're approved, you can sign in with your username and this password. We'll email you when it's ready.";
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
    if (f.elements.password.value !== f.elements.confirm.value) { $("accessMsg").textContent = "The two passwords don't match. Type the same password in both boxes."; f.elements.confirm.focus(); return; }
    var data = { email: f.elements.email.value.trim(), name: f.elements.name.value.trim(), username: f.elements.username.value.trim(), password: f.elements.password.value };
    if (!jellyfin) data.connect_username = data.email;
    $("accessMsg").textContent = "Submitting your request…";
    api("/api/v1/access/request", { method: "POST", body: data }).then(function (r) { f.reset(); f.hidden = true; $("accessMsg").textContent = "Request sent. Watch your email: we'll write when it's approved" + (jellyfin ? "" : ", and Emby will send one to link your account") + ". If nothing arrives, check your spam or junk folder."; }).catch(function (err) { $("accessMsg").textContent = err.message; });
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
  var panel = $("reqPanel"), input = $("reqInput"), reqOn = false, musicOn = false, booksOn = false, admin = false, pending = 0, view = { tab: "discover", detail: null, filter: "all", bookKind: "ebook" }, timer = null, cache = {};
  api("/api/v1/media-request/status").then(function (d) { reqOn = !!d.enabled; musicOn = !!d.music; booksOn = !!d.books; if (!reqOn) { input.placeholder = "Requests are closed"; } else if (musicOn || booksOn) { input.placeholder = "Search movies, series" + (musicOn ? ", artists" : "") + (booksOn ? ", books" : ""); } }).catch(function () { });
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
    // Close button, kept in the top corner while the panel scrolls.
    var x = el("button", "rp-x"); x.type = "button"; x.setAttribute("aria-label", "Close"); x.appendChild(icon("x", 18, "currentColor"));
    x.onclick = function (e) { e.stopPropagation(); close(); input.blur(); }; panel.appendChild(x);
    if (!reqOn) { panel.appendChild(el("p", "rp-msg", "Media requests are not open on this server.")); return; }
    if (view.detail) return detail();
    var q = input.value.trim();
    var pr = pendRow(); if (pr) panel.appendChild(pr);
    if (q.length >= 2) return results(q);
    var tabs = el("div", "rp-tabs"); tabs.setAttribute("role", "tablist");
    [["discover", "Discover"], ["movies", "Movies"], ["series", "Series"]].concat(musicOn ? [["music", "Music"]] : []).concat(booksOn ? [["books", "Books"]] : []).forEach(function (t) {
      var b = el("button", view.tab === t[0] ? "on" : "", t[1]); b.type = "button"; b.setAttribute("role", "tab"); b.setAttribute("aria-selected", view.tab === t[0] ? "true" : "false"); b.onclick = function () { view.tab = t[0]; render(); }; tabs.appendChild(b);
    });
    panel.appendChild(tabs);
    if (view.tab === "music") { panel.appendChild(el("p", "rp-msg", "Search for an artist above. Requesting an artist asks for everything they have released.")); return; }
    if (view.tab === "books") { panel.appendChild(el("p", "rp-msg", "Search for a book above by title or author, then ask for the ebook or the audiobook.")); return; }
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
    var f = el("div", "rp-filter"); [["all", "All"], ["movie", "Movies"], ["tv", "Series"]].concat(musicOn ? [["music", "Artists"]] : []).concat(booksOn ? [["books", "Books"]] : []).forEach(function (o) { var b = el("button", view.filter === o[0] ? "on" : "", o[1]); b.type = "button"; b.onclick = function () { view.filter = o[0]; render(); }; f.appendChild(b); }); head.appendChild(f);
    panel.appendChild(head);
    if (booksOn && (view.filter === "books" || view.tab === "books" && view.filter === "all")) { books(q, true); return; }
    if (musicOn && (view.filter === "all" || view.filter === "music")) artists(q, view.filter === "music");
    if (view.filter === "music") return;
    if (booksOn && view.filter === "all") books(q, false);
    var grid = el("div", "rp-grid"); grid.appendChild(el("span", "empty", "Searching…")); panel.appendChild(grid);
    api("/api/v1/media-request/search?query=" + encodeURIComponent(q)).then(function (d) {
      if (input.value.trim() !== q) return; grid.replaceChildren();
      var items = (d.results || []).filter(function (x) { return view.filter === "all" || x.mediaType === view.filter; });
      if (!items.length) grid.appendChild(el("p", "rp-msg", "Nothing found.")); items.forEach(function (x) { grid.appendChild(card(x)); });
    }).catch(function (e) { grid.replaceChildren(el("p", "rp-msg", e.message)); });
  }
  // Artists: whole artists only, the way the owner adds them to Lidarr.
  function artists(q, only) {
    var sec = el("section", "rp-artists"); if (!only) sec.appendChild(el("h3", "", "Artists")); var box = el("div", "rp-alist"); box.appendChild(el("span", "empty", "Searching…")); sec.appendChild(box); panel.appendChild(sec);
    api("/api/v1/music-request/search?query=" + encodeURIComponent(q)).then(function (d) {
      if (input.value.trim() !== q) return; box.replaceChildren();
      var items = d.results || []; if (!only) items = items.slice(0, 4);
      if (!items.length) { if (only) box.appendChild(el("p", "rp-msg", "No artists found.")); else sec.remove(); return; }
      items.forEach(function (a) {
        var r = el("div", "rp-artist"); var ph = el("span", "ph"); if (a.image) { var im = el("img"); im.src = a.image; im.alt = ""; im.loading = "lazy"; ph.appendChild(im); } r.appendChild(ph);
        var t = el("div", "t"); t.appendChild(el("b", "", a.name)); if (a.detail) t.appendChild(el("span", "", a.detail)); r.appendChild(t);
        var msg = el("span", "m");
        var b = el("button", "btn" + (a.inLibrary || a.requested ? "" : " primary"), a.inLibrary ? "In the library" : a.requested ? "Requested" : "Request artist"); b.type = "button"; b.disabled = a.inLibrary || a.requested;
        b.setAttribute("aria-label", (a.inLibrary ? "In the library: " : a.requested ? "Requested: " : "Request everything by ") + a.name);
        b.onclick = function (e) { e.stopPropagation(); b.disabled = true; msg.textContent = "Sending…";
          api("/api/v1/music-request", { method: "POST", body: { mbid: a.mbid, name: a.name, detail: a.detail || "" } }).then(function () { b.textContent = "Requested"; msg.textContent = "It waits for the admin's approval."; if (admin) pendingCount(); })
            .catch(function (er) { b.disabled = false; msg.textContent = er.message; }); };
        var act = el("div", "a"); act.appendChild(b); act.appendChild(msg); r.appendChild(act); box.appendChild(r);
      });
    }).catch(function (e) { box.replaceChildren(el("p", "rp-msg", e.message)); });
  }
  // Books: from Open Library; the request goes to the admin, then to BookOrbit.
  function books(q, only) {
    var sec = el("section", "rp-artists"); var hd = el("div", "rp-bhead"); if (!only) hd.appendChild(el("h3", "", "Books"));
    var kinds = el("div", "rp-filter"); [["ebook", "Ebook"], ["audiobook", "Audiobook"]].forEach(function (o) { var b = el("button", view.bookKind === o[0] ? "on" : "", o[1]); b.type = "button"; b.setAttribute("aria-pressed", view.bookKind === o[0] ? "true" : "false"); b.onclick = function (e) { e.stopPropagation(); view.bookKind = o[0]; render(); }; kinds.appendChild(b); });
    hd.appendChild(kinds); sec.appendChild(hd);
    var box = el("div", "rp-alist"); box.appendChild(el("span", "empty", "Searching…")); sec.appendChild(box); panel.appendChild(sec);
    var kind = view.bookKind;
    api("/api/v1/book-request/search?kind=" + kind + "&query=" + encodeURIComponent(q)).then(function (d) {
      if (input.value.trim() !== q) return; box.replaceChildren();
      var items = d.results || []; if (!only) items = items.slice(0, 4);
      if (!items.length) { if (only) box.appendChild(el("p", "rp-msg", "No books found.")); else sec.remove(); return; }
      items.forEach(function (a) {
        var r = el("div", "rp-artist rp-book"); var ph = el("span", "ph"); if (a.image) { var im = el("img"); im.src = a.image; im.alt = ""; im.loading = "lazy"; ph.appendChild(im); } r.appendChild(ph);
        var t = el("div", "t"); t.appendChild(el("b", "", a.title)); var sub = [(a.authors || []).join(", "), a.year || ""].filter(Boolean).join(" · "); if (sub) t.appendChild(el("span", "", sub)); r.appendChild(t);
        var msg = el("span", "m"), word = kind === "audiobook" ? "audiobook" : "ebook";
        var b = el("button", "btn" + (a.inLibrary || a.requested ? "" : " primary"), a.inLibrary ? "In the library" : a.requested ? "Requested" : "Request " + word); b.type = "button"; b.disabled = a.inLibrary || a.requested;
        b.setAttribute("aria-label", (a.inLibrary ? "In the library: " : a.requested ? "Requested: " : "Request the " + word + " of ") + a.title);
        b.onclick = function (e) { e.stopPropagation(); b.disabled = true; msg.textContent = "Sending…";
          api("/api/v1/book-request", { method: "POST", body: { key: a.key, title: a.title, authors: a.authors || [], year: a.year || 0, isbn13: a.isbn13 || "", cover: a.cover || "", kind: kind } }).then(function () { b.textContent = "Requested"; msg.textContent = "It waits for the admin's approval."; if (admin) pendingCount(); })
            .catch(function (er) { b.disabled = false; msg.textContent = er.message; }); };
        var act = el("div", "a"); act.appendChild(b); act.appendChild(msg); r.appendChild(act); box.appendChild(r);
      });
    }).catch(function (e) { box.replaceChildren(el("p", "rp-msg", e.message)); });
  }
  function detail() {
    var dv = view.detail, x = panel.firstChild; panel.appendChild(el("p", "rp-msg", "Loading…"));
    api("/api/v1/media-request/title?type=" + dv.type + "&id=" + dv.id).then(function (t) { if (view.detail !== dv) return; panel.replaceChildren(x); titlePage(t); }).catch(function () { if (view.detail !== dv) return; panel.replaceChildren(x); simplePage(dv.fallback, dv.type); });
  }
  function backBtn() { var b = el("button", "td-backbtn", "← Back"); b.type = "button"; b.onclick = function (e) { e.stopPropagation(); view.detail = null; render(); panel.scrollTop = 0; }; return b; }
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
