(function () {
  "use strict";

  /* ---------------- i18n ---------------- */

  // The server renders every label into the page and also ships the strings
  // that describe a running transfer, because those are only produced after
  // it has loaded. They arrive with their printf placeholders intact; filling
  // them in order keeps one convention across Go and JS.
  var T = window.__T || {};

  function t(key, args) {
    var s = T[key];
    if (s === undefined) return key;
    if (!args) return s;
    var i = 0;
    return s.replace(/%[sd]/g, function () {
      return i < args.length ? String(args[i++]) : "";
    });
  }

  var body = document.body;
  var shareId = body.getAttribute("data-share") || "";
  var cwd = body.getAttribute("data-dir") || "";
  var readOnly = body.getAttribute("data-readonly") === "1";
  // Stamped by the server on the way out. A tab that was opened before the
  // binary was rebuilt keeps running the old script, and every server-side fix
  // then looks broken; comparing ids lets the page reload itself.
  var PAGE_BUILD = body.getAttribute("data-build") || "";

  var tasks = [];
  var taskSeq = 0;
  var uploading = false;
  // The user pressed "hide". Progress updates must not pop the panel back open
  // — that made the button look broken — so the flag wins until a new batch.
  var sheetHidden = false;
  // Bumped by "cancel all". Every loop that could still be running compares
  // against it and stops, which is what makes cancellation instant instead of
  // "the current chunk finishes eventually".
  var uploadEpoch = 0;
  // Selected entries, keyed by share-relative path, plus the paths currently
  // rendered. Both describe this listing only, so a navigation starts clean.
  var picked = {};
  var entries = [];
  // Mirrors maxZipPaths in zip.go. The batch is posted as a form, and a form
  // response replaces the page — so an over-large selection has to be refused
  // here, before it ever leaves the browser.
  var MAX_PICK = 20000;

  var $main = document.getElementById("main");
  var $sheet = document.getElementById("sheet");
  var $sheetBody = document.getElementById("sheetBody");
  var $sheetTitle = document.getElementById("sheetTitle");
  var $sheetCancel = document.getElementById("sheetCancel");
  var $toast = document.getElementById("toast");
  var $fileInput = document.getElementById("fileInput");
  var $dirInput = document.getElementById("dirInput");
  var $upPill = document.getElementById("upPill");
  var $upPillText = document.getElementById("upPillText");
  var $dropHint = document.getElementById("dropHint");
  var $selBar = document.getElementById("selBar");
  var $selCount = document.getElementById("selCount");
  var $selAll = document.getElementById("selAll");
  var $zipForm = document.getElementById("zipForm");

  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  function fmtSize(n) {
    if (!n) return "0 B";
    if (n < 1024) return n + " B";
    var u = ["KB", "MB", "GB", "TB"];
    var i = -1;
    do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
    return n.toFixed(n < 10 ? 1 : 0) + " " + u[i];
  }

  var toastTimer;
  function toast(msg) {
    $toast.textContent = msg;
    $toast.classList.add("show");
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { $toast.classList.remove("show"); }, 2200);
  }

  // Anything that changes the listing has to reload it: the rows come from the
  // server. The confirmation is parked in sessionStorage so it survives the
  // reload instead of vanishing with the page that produced it.
  function reloadWithToast(msg) {
    try { sessionStorage.setItem("landrop.toast", msg); } catch (e) { /* private mode */ }
    location.reload();
  }

  /* ---------------- selection ---------------- */

  entries = Array.prototype.map.call(
    document.querySelectorAll("li[data-rel]"),
    function (li) { return li.getAttribute("data-rel"); }
  );

  function pickedList() {
    var out = [];
    for (var k in picked) {
      if (Object.prototype.hasOwnProperty.call(picked, k)) out.push(k);
    }
    return out;
  }

  function clearPicked() {
    picked = {};
    updateSelBar();
  }

  function applyPick(li, on) {
    if (!li) return;
    li.classList.toggle("sel", on);
    var box = li.querySelector("button.pick");
    if (!box) return;
    box.classList.toggle("on", on);
    box.setAttribute("aria-pressed", on ? "true" : "false");
  }

  function togglePick(li, rel) {
    if (picked[rel]) delete picked[rel];
    else picked[rel] = true;
    applyPick(li, !!picked[rel]);
    updateSelBar();
  }

  // One pass over the rendered rows. Calling applyPick per entry instead would
  // be quadratic, which a folder of a few thousand files would notice.
  function syncPicks() {
    var lis = $main.querySelectorAll("li[data-rel]");
    for (var i = 0; i < lis.length; i++) {
      applyPick(lis[i], !!picked[lis[i].getAttribute("data-rel")]);
    }
  }

  function updateSelBar() {
    var n = pickedList().length;
    if (!n) {
      $selBar.classList.remove("on");
      return;
    }
    $selBar.classList.add("on");
    $selCount.textContent = t("selectedCount", [n]);
    // Offer "clear all" only once everything visible is actually selected.
    $selAll.textContent = (entries.length > 0 && n >= entries.length) ? t("clearAll") : t("selectAll");
  }

  function toggleSelectAll() {
    var all = entries.length > 0 && pickedList().length >= entries.length;
    picked = {};
    if (!all) {
      for (var i = 0; i < entries.length; i++) picked[entries[i]] = true;
    }
    syncPicks();
    updateSelBar();
  }

  function appendHidden(form, name, value) {
    var el = document.createElement("input");
    el.type = "hidden";
    el.name = name;
    el.value = value;
    form.appendChild(el);
  }

  // Posted as a form rather than followed as a link: a batch can carry
  // thousands of paths, and that does not belong in a URL. The response sets
  // Content-Disposition, so the browser downloads it and stays on this page.
  function downloadZip() {
    var paths = pickedList();
    if (!paths.length) {
      toast(t("selectNothing"));
      return;
    }
    if (paths.length > MAX_PICK) {
      toast(t("tooManyPicked", [MAX_PICK]));
      return;
    }
    var form = $zipForm;
    form.innerHTML = "";
    appendHidden(form, "share", shareId);
    appendHidden(form, "dir", cwd);
    for (var i = 0; i < paths.length; i++) appendHidden(form, "paths", paths[i]);
    form.submit();
    toast(t("zipStarted", [paths.length]));
  }

  function api(path, timeoutMs) {
    var opts = { headers: { Accept: "application/json" } };
    var timer = 0;
    if (timeoutMs) {
      var ctl = new AbortController();
      opts.signal = ctl.signal;
      timer = setTimeout(function () { ctl.abort(); }, timeoutMs);
    }
    function done() { if (timer) { clearTimeout(timer); timer = 0; } }
    return fetch(path, opts).then(function (r) {
      return r.json().then(function (data) {
        done();
        if (!r.ok) throw new Error(data && data.error ? data.error : ("HTTP " + r.status));
        return data;
      }, function (e) { done(); throw e; });
    }, function (e) { done(); throw e; });
  }

  $main.addEventListener("click", function (ev) {
    // The three-dot trigger is the only exposed action on a row now.
    var more = ev.target.closest("button[data-more]");
    if (more) {
      ev.stopPropagation();
      openRowMenu(more);
      return;
    }

    // The checkbox column and the icon beside it are the selection hot spots.
    var pk = ev.target.closest("[data-pick]");
    if (pk) {
      ev.stopPropagation();
      ev.preventDefault();
      togglePick(pk.closest("li"), pk.getAttribute("data-pick"));
      return;
    }

    var del = ev.target.closest("button[data-del]");
    if (del) {
      ev.stopPropagation();
      deleteRow(del.getAttribute("data-del"), del.closest("li"));
    }
    // Folder names are real links; files have no click action. There is no
    // JavaScript to run for navigation, which is the point of the SSR page.
  });

  function deleteRow(rel, li) {
    if (!rel) return;
    if (!window.confirm(t("confirmDelete", [rel.split("/").pop()]))) return;
    fetch("/api/file?share=" + encodeURIComponent(shareId) + "&path=" + encodeURIComponent(rel), {
      method: "DELETE"
    })
      .then(function (r) { return r.json(); })
      .then(function (d) {
        if (d.error) throw new Error(d.error);
        if (li) li.remove();
        // Keep the selection model in step with the DOM, or "select all"
        // keeps counting a row that no longer exists.
        delete picked[rel];
        entries = entries.filter(function (x) { return x !== rel; });
        updateSelBar();
        var list = document.getElementById("fileList");
        var empty = document.getElementById("emptyState");
        if (list && empty && !list.querySelector("li[data-rel]")) {
          list.hidden = true;
          empty.hidden = false;
        }
        toast(t("deleted"));
      })
      .catch(function (e) { toast(t("deleteFailed", [e.message])); });
  }

  /* ---------------- row menu ---------------- */

  var $rowMenu = document.getElementById("rowMenu");
  var $menuView = document.getElementById("menuView");
  var $menuOpen = document.getElementById("menuOpen");
  var $menuDownload = document.getElementById("menuDownload");
  var $menuDelete = document.getElementById("menuDelete");
  var menuRow = null;

  function openRowMenu(btn) {
    closeRowMenu();
    menuRow = btn.closest("li");
    if (!menuRow) return;
    var isDir = menuRow.getAttribute("data-dir") === "1";
    $menuView.hidden = menuRow.getAttribute("data-view") !== "1";
    $menuOpen.hidden = !isDir;
    $menuDownload.hidden = isDir;
    if ($menuDelete) $menuDelete.hidden = readOnly;
    $rowMenu.hidden = false;
    btn.setAttribute("aria-expanded", "true");

    // Anchor to the trigger and keep the whole menu on screen, including when
    // the row is the last one and the usual position would fall off the bottom.
    var r = btn.getBoundingClientRect();
    var mw = $rowMenu.offsetWidth;
    var mh = $rowMenu.offsetHeight;
    var left = Math.min(r.right - mw, window.innerWidth - mw - 8);
    var top = r.bottom + 4;
    if (top + mh > window.innerHeight - 8) top = r.top - mh - 4;
    $rowMenu.style.left = Math.max(8, left) + "px";
    $rowMenu.style.top = Math.max(8, top) + "px";
  }

  function closeRowMenu() {
    if ($rowMenu.hidden) return;
    $rowMenu.hidden = true;
    var triggers = $main.querySelectorAll("button[data-more]");
    for (var i = 0; i < triggers.length; i++) triggers[i].setAttribute("aria-expanded", "false");
    menuRow = null;
  }

  document.addEventListener("pointerdown", function (ev) {
    if (!$rowMenu.hidden && !$rowMenu.contains(ev.target) && !ev.target.closest("button[data-more]")) {
      closeRowMenu();
    }
  });
  window.addEventListener("scroll", closeRowMenu, true);
  window.addEventListener("resize", closeRowMenu);

  function currentRel() {
    return menuRow ? menuRow.getAttribute("data-rel") : "";
  }

  function fileURL(rel, inline) {
    return "/api/download?share=" + encodeURIComponent(shareId) +
      "&path=" + encodeURIComponent(rel) + (inline ? "&inline=1" : "");
  }

  $menuView.addEventListener("click", function () {
    var rel = currentRel();
    closeRowMenu();
    if (rel) openViewer(rel);
  });

  $menuOpen.addEventListener("click", function () {
    var link = menuRow && menuRow.querySelector("a.meta");
    var href = link ? link.getAttribute("href") : "";
    closeRowMenu();
    if (href) location.href = href;
  });

  $menuDownload.addEventListener("click", function () {
    var rel = currentRel();
    closeRowMenu();
    if (!rel) return;
    var a = document.createElement("a");
    a.href = fileURL(rel, false);
    a.download = rel.split("/").pop();
    a.rel = "noopener";
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    toast(t("downloadStarted", [rel.split("/").pop()]));
  });

  if ($menuDelete) {
    $menuDelete.addEventListener("click", function () {
      var rel = currentRel();
      var row = menuRow;
      closeRowMenu();
      if (rel) deleteRow(rel, row);
    });
  }

  $selAll.addEventListener("click", toggleSelectAll);

  document.getElementById("selDownload").addEventListener("click", downloadZip);

  document.getElementById("selClear").addEventListener("click", function () {
    clearPicked();
    syncPicks();
  });

  if (document.getElementById("mkdirBtn")) {
    document.getElementById("mkdirBtn").addEventListener("click", function () {
      var name = window.prompt(t("newFolderName"));
      if (!name) return;
      fetch("/api/mkdir?share=" + encodeURIComponent(shareId) +
        "&dir=" + encodeURIComponent(cwd) + "&name=" + encodeURIComponent(name), { method: "POST" })
        .then(function (r) { return r.json(); })
        .then(function (d) {
          if (d.error) throw new Error(d.error);
          reloadWithToast(t("created"));
        })
        .catch(function (e) { toast(t("createFailed", [e.message])); });
    });
  }

  /* ---------------- full-screen viewer ---------------- */

  var $viewer = document.getElementById("viewer");
  var $viewerStage = document.getElementById("viewerStage");
  var $viewerImg = document.getElementById("viewerImg");
  var $viewerDoc = document.getElementById("viewerDoc");
  var $viewerFrame = document.getElementById("viewerFrame");
  var $viewerName = document.getElementById("viewerName");
  var $viewerMeta = document.getElementById("viewerMeta");
  var $viewerTools = document.getElementById("viewerTools");
  var $zoomPct = document.getElementById("zoomPct");

  var viewerMode = "";
  // A viewer opened, closed, and reopened quickly must not let the first
  // preview paint over the second one after its fetch finishes.
  var viewSeq = 0;
  var zoom = { scale: 1, x: 0, y: 0 };
  var drag = null;
  var scriptCache = {};

  function vendorURL(file) {
    return "/static/vendor/" + file + "?v=" + encodeURIComponent(PAGE_BUILD);
  }

  function loadScript(url) {
    if (scriptCache[url]) return scriptCache[url];
    scriptCache[url] = new Promise(function (resolve, reject) {
      var el = document.createElement("script");
      el.src = url;
      el.onload = resolve;
      el.onerror = reject;
      document.head.appendChild(el);
    });
    return scriptCache[url];
  }

  function resetViewer() {
    viewerMode = "";
    $viewerImg.hidden = true;
    $viewerDoc.hidden = true;
    $viewerFrame.hidden = true;
    $viewerTools.hidden = true;
    $viewerImg.removeAttribute("src");
    $viewerFrame.removeAttribute("src");
    drag = null;
  }

  function viewerNote(msg) {
    resetViewer();
    viewerMode = "note";
    $viewerDoc.hidden = false;
    $viewerDoc.innerHTML = '<div class="viewer-note">' + esc(msg) + "</div>";
  }

  function openViewer(rel) {
    var row = null;
    var lis = $main.querySelectorAll("li[data-rel]");
    for (var i = 0; i < lis.length; i++) {
      if (lis[i].getAttribute("data-rel") === rel) { row = lis[i]; break; }
    }
    var nameEl = row && row.querySelector(".name");
    var name = nameEl ? nameEl.textContent : rel.split("/").pop();
    $viewerName.textContent = name;

    // Every value here is escaped: a file name is attacker-controlled text.
    $viewerMeta.innerHTML =
      "<span><b>" + esc(t("createdAt")) + "</b> " + esc((row && row.getAttribute("data-created")) || "—") + "</span>" +
      "<span><b>" + esc(t("modified")) + "</b> " + esc((row && row.getAttribute("data-modified")) || "—") + "</span>" +
      "<span><b>" + esc(t("size")) + "</b> " + esc((row && row.getAttribute("data-size")) || "—") + "</span>";

    resetViewer();
    $viewer.hidden = false;
    var seq = ++viewSeq;
    var ext = name.split(".").pop().toLowerCase();

    if (isImageExt(ext)) return showViewerImage(rel, seq);
    if (isVideoExtJS(ext)) return viewerNote(t("videoTodo"));
    if (ext === "md" || ext === "markdown") return showMarkdown(rel, seq);
    if (ext === "pdf") {
      viewerMode = "frame";
      $viewerFrame.hidden = false;
      $viewerFrame.src = fileURL(rel, true);
      return;
    }
    if (ext === "docx") return showDocx(rel, seq);
    if (ext === "xlsx" || ext === "xls") return showSheet(rel, seq);
    return viewerNote(t("previewFailed"));
  }

  function isImageExt(ext) {
    return ["jpg", "jpeg", "png", "gif", "bmp", "webp", "tif", "tiff", "svg", "heic", "heif", "avif"].indexOf(ext) >= 0;
  }

  function isVideoExtJS(ext) {
    return ["mp4", "m4v", "mov", "mkv", "webm", "avi", "wmv", "flv", "mpg", "mpeg", "3gp"].indexOf(ext) >= 0;
  }

  function showViewerImage(rel, seq) {
    viewerMode = "image";
    $viewerImg.hidden = false;
    $viewerTools.hidden = false;
    $viewerImg.onload = function () { if (seq === viewSeq) fitImage(); };
    $viewerImg.onerror = function () { if (seq === viewSeq) viewerNote(t("previewFailed")); };
    // The full file is used here, not the 128 px thumbnail: pan and zoom need
    // the detail, and this is one image the user deliberately opened.
    $viewerImg.src = fileURL(rel, true);
  }

  function fitImage() {
    var iw = $viewerImg.naturalWidth;
    var ih = $viewerImg.naturalHeight;
    if (!iw || !ih) return;
    var sw = $viewerStage.clientWidth;
    var sh = $viewerStage.clientHeight;
    var s = Math.min(sw / iw, sh / ih, 1);
    zoom.scale = s;
    zoom.x = (sw - iw * s) / 2;
    zoom.y = (sh - ih * s) / 2;
    applyZoom();
  }

  function applyZoom() {
    $viewerImg.style.transform =
      "translate(" + zoom.x + "px," + zoom.y + "px) scale(" + zoom.scale + ")";
    $zoomPct.textContent = Math.round(zoom.scale * 100) + "%";
  }

  // Zoom around a point instead of the origin, so the pixel under the cursor
  // stays put. Otherwise a wheel scroll makes the image run away.
  function setZoomAt(px, py, next) {
    next = Math.max(0.05, Math.min(12, next));
    var wx = (px - zoom.x) / zoom.scale;
    var wy = (py - zoom.y) / zoom.scale;
    zoom.x = px - wx * next;
    zoom.y = py - wy * next;
    zoom.scale = next;
    applyZoom();
  }

  function zoomAtCenter(factor) {
    setZoomAt($viewerStage.clientWidth / 2, $viewerStage.clientHeight / 2, zoom.scale * factor);
  }

  $viewerStage.addEventListener("wheel", function (e) {
    if (viewerMode !== "image" || $viewerImg.hidden) return;
    e.preventDefault();
    var r = $viewerStage.getBoundingClientRect();
    setZoomAt(e.clientX - r.left, e.clientY - r.top,
      zoom.scale * Math.exp(-e.deltaY * 0.0015));
  }, { passive: false });

  $viewerStage.addEventListener("pointerdown", function (e) {
    if (viewerMode !== "image" || $viewerImg.hidden) return;
    drag = { id: e.pointerId, x: e.clientX, y: e.clientY, ox: zoom.x, oy: zoom.y };
    try { $viewerStage.setPointerCapture(e.pointerId); } catch (_) { /* older browser */ }
    $viewerImg.classList.add("dragging");
  });
  $viewerStage.addEventListener("pointermove", function (e) {
    if (!drag || e.pointerId !== drag.id) return;
    zoom.x = drag.ox + (e.clientX - drag.x);
    zoom.y = drag.oy + (e.clientY - drag.y);
    applyZoom();
  });
  function endDrag(e) {
    if (!drag || (e && e.pointerId !== drag.id)) return;
    drag = null;
    $viewerImg.classList.remove("dragging");
  }
  $viewerStage.addEventListener("pointerup", endDrag);
  $viewerStage.addEventListener("pointercancel", endDrag);
  $viewerImg.addEventListener("dblclick", function () {
    if (viewerMode !== "image") return;
    if (Math.abs(zoom.scale - 1) < 0.01) fitImage();
    else setZoomAt($viewerStage.clientWidth / 2, $viewerStage.clientHeight / 2, 1);
  });

  document.getElementById("zoomIn").addEventListener("click", function () { zoomAtCenter(1.25); });
  document.getElementById("zoomOut").addEventListener("click", function () { zoomAtCenter(0.8); });
  document.getElementById("zoomFit").addEventListener("click", fitImage);

  async function showMarkdown(rel, seq) {
    viewerMode = "doc";
    $viewerDoc.hidden = false;
    $viewerDoc.innerHTML = '<div class="viewer-note">' + esc(t("previewLoading")) + "</div>";
    try {
      await Promise.all([
        loadScript(vendorURL("marked.min.js")),
        loadScript(vendorURL("purify.min.js")),
      ]);
      if (seq !== viewSeq) return;
      var res = await fetch(fileURL(rel, true));
      if (!res.ok) throw new Error("HTTP " + res.status);
      var text = await res.text();
      if (seq !== viewSeq) return;
      // Markdown may contain raw HTML. DOMPurify is not decoration here.
      var html = window.marked.parse(text, { gfm: true });
      $viewerDoc.innerHTML = window.DOMPurify.sanitize(html);
    } catch (e) {
      if (seq === viewSeq) viewerNote(t("previewLibFail"));
    }
  }

  async function showDocx(rel, seq) {
    viewerMode = "doc";
    $viewerDoc.hidden = false;
    $viewerDoc.innerHTML = '<div class="viewer-note">' + esc(t("previewLoading")) + "</div>";
    try {
      await Promise.all([
        loadScript(vendorURL("mammoth.browser.min.js")),
        loadScript(vendorURL("purify.min.js")),
      ]);
      if (seq !== viewSeq) return;
      var res = await fetch(fileURL(rel, true));
      if (!res.ok) throw new Error("HTTP " + res.status);
      var buf = await res.arrayBuffer();
      if (seq !== viewSeq) return;
      var out = await window.mammoth.convertToHtml({ arrayBuffer: buf });
      if (seq !== viewSeq) return;
      $viewerDoc.innerHTML = window.DOMPurify.sanitize(out.value);
    } catch (e) {
      if (seq === viewSeq) viewerNote(t("previewLibFail"));
    }
  }

  async function showSheet(rel, seq) {
    viewerMode = "doc";
    $viewerDoc.hidden = false;
    $viewerDoc.innerHTML = '<div class="viewer-note">' + esc(t("previewLoading")) + "</div>";
    try {
      await loadScript(vendorURL("xlsx.mini.min.js"));
      if (seq !== viewSeq) return;
      var res = await fetch(fileURL(rel, true));
      if (!res.ok) throw new Error("HTTP " + res.status);
      var buf = await res.arrayBuffer();
      if (seq !== viewSeq) return;
      var wb = window.XLSX.read(new Uint8Array(buf), { type: "array" });
      var ws = wb.Sheets[wb.SheetNames[0]];
      if (!ws) throw new Error("empty workbook");
      var rows = window.XLSX.utils.sheet_to_json(ws, { header: 1, raw: false, defval: "" });
      var limit = 500;
      var html = rows.length > limit
        ? '<p class="sheet-note">' + esc(t("rowsOmitted", [limit])) + "</p>"
        : "";
      html += "<table><tbody>";
      for (var i = 0; i < Math.min(rows.length, limit); i++) {
        html += "<tr>";
        var row = rows[i] || [];
        for (var j = 0; j < Math.min(row.length, 40); j++) {
          html += "<td>" + esc(String(row[j] == null ? "" : row[j])) + "</td>";
        }
        html += "</tr>";
      }
      html += "</tbody></table>";
      if (seq === viewSeq) $viewerDoc.innerHTML = html;
    } catch (e) {
      if (seq === viewSeq) viewerNote(t("previewFailed"));
    }
  }

  function closeViewer() {
    ++viewSeq;
    $viewer.hidden = true;
    resetViewer();
    $viewerDoc.innerHTML = "";
  }

  document.getElementById("viewerClose").addEventListener("click", closeViewer);

  document.addEventListener("keydown", function (e) {
    if (!$viewer.hidden) {
      if (e.key === "Escape") { e.preventDefault(); closeViewer(); return; }
      if (viewerMode === "image") {
        if (e.key === "+" || e.key === "=") { e.preventDefault(); zoomAtCenter(1.25); }
        else if (e.key === "-") { e.preventDefault(); zoomAtCenter(0.8); }
        else if (e.key === "0") { e.preventDefault(); fitImage(); }
      }
      return;
    }
    if (e.key === "Escape") closeRowMenu();
  });

  /* ---------------- upload queue ---------------- */

  function bindUploadButton(id, input) {
    var btn = document.getElementById(id);
    if (!btn || !input) return;
    btn.addEventListener("click", function () { input.click(); });
  }

  // enqueue turns picked or dropped files into queued tasks. dir is the folder
  // path relative to whatever the user selected, so a dropped folder keeps its
  // shape instead of dumping every file into the current directory.
  function enqueue(items) {
    if (!items || !items.length) return;
    for (var i = 0; i < items.length; i++) {
      var it = items[i];
      tasks.push({
        id: ++taskSeq,
        file: it.file,
        share: shareId,
        dir: it.dir ? (cwd ? cwd + "/" + it.dir : it.dir) : cwd,
        loaded: 0,
        total: it.file.size,
        state: "wait",
        speed: 0,
        err: "",
        lastTime: 0,
        lastLoaded: 0
      });
    }

    // Keep the list bounded: the sheet rebuilds every row on each progress
    // tick, so an unbounded array quietly slows down a long session.
    while (tasks.length > 60) {
      var dropAt = -1;
      for (var d = 0; d < tasks.length; d++) {
        if (tasks[d].state === "done" || tasks[d].state === "err") {
          dropAt = d;
          break;
        }
      }
      if (dropAt < 0) break;
      tasks.splice(dropAt, 1);
    }

    sheetHidden = false; // a fresh batch should show itself
    renderSheet();
    pump();
  }

  if ($fileInput) {
    $fileInput.addEventListener("change", function () {
      var files = Array.prototype.slice.call($fileInput.files || []);
      $fileInput.value = "";
      if (!files.length) return;
      enqueue(files.map(function (f) { return { file: f, dir: "" }; }));
    });
  }

  // A folder picker only reports files, each carrying its path relative to the
  // selected root. The server creates the missing directories as the uploads
  // land, so nothing else is needed here.
  if ($dirInput) {
    $dirInput.addEventListener("change", function () {
      var files = Array.prototype.slice.call($dirInput.files || []);
      $dirInput.value = "";
      if (!files.length) return;
      enqueue(files.map(function (f) {
        var rel = f.webkitRelativePath || "";
        var cut = rel.lastIndexOf("/");
        return { file: f, dir: cut < 0 ? "" : rel.slice(0, cut) };
      }));
    });
  }

  bindUploadButton("uploadBtn", $fileInput);
  bindUploadButton("uploadDirBtn", $dirInput);

  /* ---------------- drag and drop ---------------- */

  function hasFiles(e) {
    var dt = e.dataTransfer;
    if (!dt) return false;
    var types = dt.types || [];
    for (var i = 0; i < types.length; i++) {
      if (types[i] === "Files") return true;
    }
    return false;
  }

  // readEntries returns at most 100 entries per call, so a folder has to be
  // drained in a loop until it reports empty.
  function walkEntry(entry, dir, out) {
    return new Promise(function (resolve) {
      if (entry.isFile) {
        entry.file(function (f) {
          out.files.push({ file: f, dir: dir });
          resolve();
        }, function () { resolve(); });
        return;
      }
      if (!entry.isDirectory) {
        resolve();
        return;
      }
      var sub = dir ? dir + "/" + entry.name : entry.name;
      out.dirs.push(sub);
      var reader = entry.createReader();
      var pendingReads = [];
      (function readBatch() {
        reader.readEntries(function (batch) {
          if (!batch.length) {
            Promise.all(pendingReads).then(function () { resolve(); });
            return;
          }
          for (var i = 0; i < batch.length; i++) {
            pendingReads.push(walkEntry(batch[i], sub, out));
          }
          readBatch();
        }, function () { resolve(); });
      })();
    });
  }

  // An empty folder contains no files to create it, so it gets an explicit
  // mkdir. Best effort: a failure costs an empty directory, nothing more.
  function ensureDir(rel) {
    var full = cwd ? cwd + "/" + rel : rel;
    var cut = full.lastIndexOf("/");
    var parent = cut < 0 ? "" : full.slice(0, cut);
    var leaf = cut < 0 ? full : full.slice(cut + 1);
    if (!leaf) return;
    fetch("/api/mkdir?share=" + encodeURIComponent(shareId) +
      "&dir=" + encodeURIComponent(parent) + "&name=" + encodeURIComponent(leaf),
      { method: "POST" }).catch(function () { /* the upload recreates it */ });
  }

  async function dropUpload(dt) {
    var items = dt.items;
    if (!items || !items.length || typeof items[0].webkitGetAsEntry !== "function") {
      // Older browsers: files only, folders arrive as a flat list.
      enqueue(Array.prototype.slice.call(dt.files || []).map(function (f) {
        return { file: f, dir: "" };
      }));
      return;
    }
    var list = [];
    for (var i = 0; i < items.length; i++) {
      if (items[i].kind !== "file") continue;
      var entry = items[i].webkitGetAsEntry();
      if (entry) list.push(entry);
    }
    var out = { files: [], dirs: [] };
    await Promise.all(list.map(function (e) { return walkEntry(e, "", out); }));
    out.dirs.forEach(function (d) { ensureDir(d); });
    if (out.files.length) enqueue(out.files);
  }

  var dragDepth = 0;
  window.addEventListener("dragenter", function (e) {
    if (!hasFiles(e)) return;
    e.preventDefault();
    dragDepth++;
    if (shareId && !readOnly) $dropHint.hidden = false;
  });
  window.addEventListener("dragover", function (e) {
    if (!hasFiles(e)) return;
    e.preventDefault(); // without this the drop event never fires
    if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
  });
  window.addEventListener("dragleave", function (e) {
    if (!hasFiles(e)) return;
    dragDepth = Math.max(0, dragDepth - 1);
    if (!dragDepth) $dropHint.hidden = true;
  });
  window.addEventListener("drop", function (e) {
    if (!hasFiles(e)) return;
    e.preventDefault(); // otherwise the browser opens the file instead
    dragDepth = 0;
    $dropHint.hidden = true;
    if (!shareId) {
      toast(t("enterShare"));
      return;
    }
    if (readOnly) return;
    dropUpload(e.dataTransfer).catch(function () { /* nothing usable */ });
  });

  /* ---------------- sheet ---------------- */

  document.getElementById("sheetClose").addEventListener("click", function () {
    sheetHidden = true;
    $sheet.classList.remove("show");
    renderSheet(); // paints the pill straight away
  });

  $upPill.addEventListener("click", function () {
    sheetHidden = false;
    renderSheet();
  });

  $sheetCancel.addEventListener("click", cancelAllUploads);

  // Closing the tab mid-transfer is the most expensive mistake available here:
  // the bytes already on the server survive, but the queue does not. Ask first.
  window.addEventListener("beforeunload", function (e) {
    if (!uploading) return;
    e.preventDefault();
    e.returnValue = "";
  });

  function probe(share, dir, name, total) {
    var url = "/api/probe?share=" + encodeURIComponent(share) +
      "&dir=" + encodeURIComponent(dir) + "&name=" + encodeURIComponent(name);
    // The total lets the server answer "this file is already complete", which
    // is the only reliable way to skip a re-upload of something it already has.
    if (total) url += "&total=" + encodeURIComponent(total);
    return api(url);
  }

  // Uploaded in chunks on purpose. One 5 GB request means a hiccup at 99% costs
  // the entire request. 32 MB caps what a single failure can waste and keeps
  // the checksum pass's working set small enough for a phone: each chunk is
  // held in memory once so it can be hashed and sent without a second read.
  var CHUNK_SIZE = 32 * 1024 * 1024;
  // No upload progress for this long means the socket is dead. TCP itself can
  // take fifteen minutes to admit that, which is an eternity in front of a
  // screen. A watchdog aborts and lets the retry logic resume.
  var IDLE_TIMEOUT = 45000;
  // Consecutive attempts that advance zero bytes before giving up. A transfer
  // that keeps making progress is never abandoned, however flaky the link.
  var MAX_STALLED = 12;

  function newUploadId() {
    return Date.now().toString(36) + Math.random().toString(36).slice(2, 10);
  }

  // CRC32 over the exact bytes we are about to send. TCP's own checksum is 16
  // bits and is usually computed by the network card, so it catches far less
  // than people assume; this is the cheap backstop that turns "the file
  // arrived and nobody noticed it was wrong" into a retryable error.
  var CRC_TABLE = (function () {
    var t = new Uint32Array(256);
    for (var n = 0; n < 256; n++) {
      var c = n;
      for (var k = 0; k < 8; k++) c = c & 1 ? 0xEDB88320 ^ (c >>> 1) : c >>> 1;
      t[n] = c >>> 0;
    }
    return t;
  })();

  function crc32(bytes) {
    var c = 0xFFFFFFFF;
    for (var i = 0; i < bytes.length; i++) {
      c = (c >>> 8) ^ CRC_TABLE[(c ^ bytes[i]) & 0xFF];
    }
    return (c ^ 0xFFFFFFFF) >>> 0;
  }

  // putChunk sends bytes [offset, end) and resolves with the server's byte
  // count, which is the only number worth trusting for the next offset.
  function putChunk(task, offset, end, epoch, onProgress) {
    return new Promise(function (resolve, reject) {
      var xhr = new XMLHttpRequest();
      var watchdog = 0;
      var settled = false;
      // Exposed on the task so "cancel all" can abort this exact request
      // instead of waiting for the current chunk to finish.
      task.xhr = xhr;

      function settle(fn, value) {
        if (settled) return;
        settled = true;
        clearTimeout(watchdog);
        if (task.xhr === xhr) task.xhr = null;
        fn(value);
      }
      function arm(ms) {
        clearTimeout(watchdog);
        watchdog = setTimeout(function () {
          try { xhr.abort(); } catch (_) { /* already gone */ }
          settle(reject, new Error(t("stalled")));
        }, ms || IDLE_TIMEOUT);
      }

      xhr.open("PUT", "/api/upload?share=" + encodeURIComponent(task.share) +
        "&dir=" + encodeURIComponent(task.dir) + "&name=" + encodeURIComponent(task.file.name));
      xhr.setRequestHeader("X-Offset", String(offset));
      xhr.setRequestHeader("X-Total-Size", String(task.total));
      // The fingerprint lets the server refuse to resume into bytes that came
      // from a different file that happened to share this name.
      xhr.setRequestHeader("X-File-Size", String(task.total));
      xhr.setRequestHeader("X-File-Mtime", String(task.file.lastModified || 0));
      // One transfer, many requests: the id keeps the desktop activity list
      // showing a single row. The server timestamps the first chunk itself —
      // a clock from this device would just import its skew into the speed.
      xhr.setRequestHeader("X-Upload-Id", task.uploadId);
      xhr.setRequestHeader("Content-Type", "application/octet-stream");

      xhr.upload.onprogress = function (e) {
        arm();
        if (e.lengthComputable) onProgress(offset + e.loaded);
      };
      // Body fully sent. The server may now be flushing several gigabytes to
      // disk before it answers, so give it a much longer leash than a socket
      // that has stopped moving data deserves.
      xhr.upload.onload = function () { arm(300000); };
      xhr.onload = function () {
        var res = null;
        try { res = JSON.parse(xhr.responseText); } catch (_) { /* ignore */ }
        if (xhr.status === 200 && res) {
          settle(resolve, { size: res.size, complete: !!res.complete });
        } else if (xhr.status === 409 && res) {
          settle(resolve, { conflict: true, reset: !!res.reset, busy: !!res.busy, size: res.size || 0 });
        } else {
          settle(reject, new Error((res && res.error) || ("HTTP " + xhr.status)));
        }
      };
      xhr.onerror = function () { settle(reject, new Error(t("connLost"))); };
      xhr.onabort = function () { settle(reject, new Error(t("stalled"))); };
      xhr.ontimeout = function () { settle(reject, new Error(t("stalled"))); };
      xhr.timeout = 0; // the watchdog above is the real limit

      // Read the chunk once and send those same bytes, rather than handing the
      // Blob to the browser and hashing a second copy: on a slow source disk a
      // second read would double the very bottleneck this tool already has.
      task.file.slice(offset, end).arrayBuffer().then(function (buf) {
        if (settled) return;
        if (stale(epoch)) {
          settle(reject, new Error(t("cancelled")));
          return;
        }
        xhr.setRequestHeader("X-Chunk-CRC32", String(crc32(new Uint8Array(buf))));
        arm();
        xhr.send(buf);
      }, function () {
        settle(reject, new Error(t("readFailed")));
      });
    });
  }

  function sleep(ms) {
    return new Promise(function (r) { setTimeout(r, ms); });
  }

  // Exponential backoff with jitter, capped so a long outage still recovers
  // within seconds of the network coming back.
  function backoffMs(stalled) {
    var base = Math.min(15000, 800 * Math.pow(2, Math.max(0, stalled - 1)));
    return base + Math.floor(Math.random() * 400);
  }

  // A dropped link is the most common reason an upload dies on a phone or a
  // laptop. Wait for the OS to say it is back instead of burning attempts
  // against a network that is not there.
  function waitOnline(ms) {
    return new Promise(function (resolve) {
      if (navigator.onLine !== false) { resolve(); return; }
      var done = false;
      function go() {
        if (done) return;
        done = true;
        window.removeEventListener("online", go);
        resolve();
      }
      window.addEventListener("online", go);
      setTimeout(go, ms);
    });
  }

  async function uploadOne(task, epoch) {
    task.state = "run";
    task.err = "";
    task.lastTime = Date.now();
    task.lastLoaded = 0;
    renderSheet();

    if (stale(epoch)) return;

    var offset = 0;
    var probed = false;
    try {
      var p = await probe(task.share, task.dir, task.file.name, task.total);
      probed = true;
      if (p.complete) {
        task.state = "done";
        task.loaded = task.total;
        renderSheet();
        return;
      }
      offset = p.size || 0;
      if (offset > 0) toast(t("resuming", [task.file.name]));
    } catch (_) {
      // Unknown, not zero. Starting at 0 would truncate whatever the server
      // already staged, so the probe is retried inside the loop instead.
    }

    if (!task.uploadId) task.uploadId = newUploadId();

    task.loaded = offset;
    var stalled = 0;
    var lastOffset = offset;
    var lastPaint = 0;

    while (true) {
      if (stale(epoch)) return;
      await waitOnline(15000);
      if (stale(epoch)) return;

      var end = Math.min(offset + CHUNK_SIZE, task.total);
      try {
        if (!probed) {
          var first = await probe(task.share, task.dir, task.file.name, task.total);
          probed = true;
          if (first.complete) {
            task.state = "done";
            task.loaded = task.total;
            task.err = "";
            renderSheet();
            return;
          }
          offset = first.size || 0;
          lastOffset = offset;
          task.loaded = offset;
          renderSheet();
          end = Math.min(offset + CHUNK_SIZE, task.total);
        }

        var res = await putChunk(task, offset, end, epoch, function (loaded) {
          var now = Date.now();
          var dt = now - task.lastTime;
          if (dt > 400) {
            task.speed = (loaded - task.lastLoaded) * 1000 / dt;
            task.lastTime = now;
            task.lastLoaded = loaded;
          }
          task.loaded = loaded;
          if (now - lastPaint > 110) {
            lastPaint = now;
            renderSheet();
          }
        });

        if (res.reset) {
          // The staged bytes belong to a different file with the same name.
          // Start over rather than splice two files together.
          offset = 0;
          lastOffset = 0;
          task.loaded = 0;
          task.err = "";
          renderSheet();
          continue;
        }
        if (res.conflict) {
          // Another device is mid-upload on this exact file. Waiting beats
          // hammering: its chunks are landing, ours would only fight them.
          if (res.busy) {
            stalled++;
            task.err = t("busy");
            renderSheet();
            if (stalled > MAX_STALLED) throw new Error(t("busy"));
            await sleep(backoffMs(stalled));
            continue;
          }
          // The server's byte count is the only one that matters.
          var advanced = res.size !== lastOffset;
          if (!advanced) stalled++;
          offset = res.size;
          lastOffset = offset;
          task.loaded = offset;
          renderSheet();
          if (stalled > MAX_STALLED) throw new Error(t("connLost"));
          // No progress means retrying instantly would just be noise.
          if (!advanced) await sleep(backoffMs(stalled));
          continue;
        }

        offset = res.size;
        task.loaded = offset;
        // Do not zero the speed here: at 32 MB per chunk that would make the
        // readout flicker to nothing roughly once a second. Resetting the
        // window is enough for the next delta to stay honest.
        task.lastTime = Date.now();
        task.lastLoaded = offset;
        renderSheet();

        if (res.complete || offset >= task.total) {
          // Never trust "done" alone: a truncated file that is reported as
          // complete is worse than a failed transfer, because nobody retries.
          if (offset !== task.total) throw new Error(t("sizeMismatch"));
          task.state = "done";
          task.loaded = task.total;
          task.err = "";
          renderSheet();
          return;
        }

        stalled = 0;
        lastOffset = offset;
      } catch (e) {
        // The abort we caused ourselves is not a failure worth retrying.
        if (stale(epoch)) return;
        stalled++;
        // "Connection lost" is a lie when the service was stopped on the
        // desktop; the watcher knows which one it is.
        task.err = serverDown
          ? t("serverDown")
          : stalled > 1
            ? t("retrying", [stalled, e.message])
            : e.message;
        renderSheet();

        if (stalled > MAX_STALLED) {
          task.state = "err";
          renderSheet();
          return;
        }
        await sleep(backoffMs(stalled));
        if (stale(epoch)) return;

        // Ask how much actually landed: a request can die after its bytes were
        // written, and resuming from the client's stale idea of the offset
        // would duplicate a chunk.
        try {
          var again = await probe(task.share, task.dir, task.file.name, task.total);
          if (again.complete) {
            task.state = "done";
            task.loaded = task.total;
            task.err = "";
            renderSheet();
            return;
          }
          var seen = again.size || 0;
          if (seen > lastOffset) stalled = 0; // progress clears the give-up counter
          offset = seen;
          lastOffset = offset;
          task.loaded = offset;
          task.lastLoaded = offset;
          task.lastTime = Date.now();
        } catch (_) { /* still unreachable; keep the offset we had */ }
      }
    }
  }

  function stale(epoch) {
    return epoch !== uploadEpoch;
  }

  // The page cannot be told that the service was stopped — there is nobody
  // left to tell it. A light poll is the next best thing: it turns a minute of
  // silent retries into a visible "service stopped" within a few seconds.
  var serverDown = false;

  async function watchServer(epoch) {
    while (!stale(epoch) && uploading) {
      await sleep(4000);
      if (stale(epoch) || !uploading) return;
      try {
        // Bounded: a stopped listener should fail fast, but a firewall that
        // drops packets instead of resetting them would hang the check for
        // minutes and the page would never admit the service is gone.
        await api("/api/info", 3000);
        if (serverDown) {
          serverDown = false;
          renderSheet();
        }
      } catch (_) {
        if (!serverDown) {
          serverDown = true;
          renderSheet();
        }
      }
    }
  }

  // postCancel tells the server a transfer is being abandoned on purpose, so
  // its final word is "cancelled" instead of the "client disconnected" it can
  // only infer from a socket that dies. Bounded by a timeout: a stopped
  // service must not make the cancel button feel broken.
  function postCancel(id) {
    var ctl = new AbortController();
    var timer = setTimeout(function () { ctl.abort(); }, 2000);
    return fetch("/api/upload/cancel?id=" + encodeURIComponent(id), {
      method: "POST",
      keepalive: true,
      signal: ctl.signal,
    })
      .catch(function () { /* the server may already be gone */ })
      .then(function () { clearTimeout(timer); });
  }

  // cancelAllUploads drops the whole queue: the chunk in flight is aborted,
  // the ones behind it never start. Bytes already on the server are left
  // alone — they live in the staging file, so picking the same file again
  // resumes instead of restarting.
  async function cancelAllUploads() {
    var active = 0;
    var ids = [];
    for (var i = 0; i < tasks.length; i++) {
      if (tasks[i].state === "run" || tasks[i].state === "wait") {
        active++;
        if (tasks[i].uploadId) ids.push(tasks[i].uploadId);
      }
    }
    if (!active) return;
    if (!window.confirm(t("cancelAllAsk"))) return;

    uploadEpoch++;
    // Announce first, abort second: reversing the order would let the server
    // report a dropped connection before the cancellation arrives.
    if (ids.length) await Promise.all(ids.map(postCancel));

    for (var j = 0; j < tasks.length; j++) {
      if (tasks[j].xhr) {
        try { tasks[j].xhr.abort(); } catch (_) { /* already finished */ }
        tasks[j].xhr = null;
      }
    }
    // Finished transfers are history and stay; everything else is gone.
    tasks = tasks.filter(function (x) { return x.state === "done"; });
    uploading = false;
    serverDown = false;
    renderSheet();
    toast(t("uploadCancelled"));
  }

  async function pump() {
    if (uploading) return;
    uploading = true;
    var epoch = uploadEpoch;
    $sheet.classList.add("show");
    watchServer(epoch);
    while (epoch === uploadEpoch) {
      var next = null;
      for (var i = 0; i < tasks.length; i++) {
        if (tasks[i].state === "wait") { next = tasks[i]; break; }
      }
      if (!next) break;
      await uploadOne(next, epoch);
    }
    // A cancel already reset the flag and the queue; touching either here
    // would clobber an upload the user started right after.
    if (stale(epoch)) return;
    uploading = false;

    var failed = tasks.filter(function (x) { return x.state === "err"; }).length;
    var doneCount = tasks.filter(function (x) { return x.state === "done"; }).length;
    // The rows came from the server, so anything that landed is only visible
    // after it renders the folder again. The confirmation is parked in
    // sessionStorage and replayed after the reload — a failure notice would
    // otherwise vanish with the page that produced it.
    if (doneCount) {
      reloadWithToast(failed
        ? t("filesFailed", [failed])
        : t("filesUploaded", [doneCount]));
      return;
    }
    if (failed) toast(t("filesFailed", [failed]));

    setTimeout(function () {
      if (!uploading) {
        tasks = tasks.filter(function (x) { return x.state === "err"; });
        renderSheet();
        if (!tasks.length) $sheet.classList.remove("show");
      }
    }, 2600);
  }

  function renderSheet() {
    if (!tasks.length) {
      $sheet.classList.remove("show");
      $sheetBody.innerHTML = "";
      $sheetCancel.hidden = true;
      $sheetTitle.textContent = t("upload");
      $upPill.hidden = true;
      sheetHidden = false;
      return;
    }

    var done = tasks.filter(function (x) { return x.state === "done"; }).length;
    var running = tasks.filter(function (x) { return x.state === "run"; }).length;
    var pending = tasks.filter(function (x) { return x.state === "run" || x.state === "wait"; }).length;
    // Nothing to cancel once the queue has drained; a button that does nothing
    // is worse than no button.
    $sheetCancel.hidden = pending === 0;
    $sheetTitle.textContent = serverDown
      ? t("serverDown")
      : running > 0
        ? t("uploading", [done, tasks.length])
        : (done === tasks.length ? t("uploadDone") : t("uploadCount", [done, tasks.length]));

    var html = "";
    for (var i = 0; i < tasks.length; i++) {
      // Named "task" rather than "t": "t" is the translation function.
      var task = tasks[i];
      var pct = task.total ? Math.min(100, Math.round(task.loaded / task.total * 100)) : 0;
      var cls = task.state === "done" ? " done" : (task.state === "err" ? " err" : (task.state === "run" ? " run" : ""));
      var sub;
      if (task.state === "done") sub = t("done", [fmtSize(task.total)]);
      else if (task.state === "err") sub = t("failed", [task.err || t("unknownError")]);
      else if (task.state === "wait") sub = t("waiting", [fmtSize(task.total)]);
      else if (task.err) sub = task.err; // retrying: the reason beats a byte count
      else {
        sub = fmtSize(task.loaded) + " / " + fmtSize(task.total);
        if (task.speed > 0) sub += " · " + fmtSize(task.speed) + "/s";
      }

      html += '<div class="task' + cls + '">' +
        '<div class="task-head"><span class="task-name">' + esc(task.file.name) + "</span>" +
        '<span class="task-pct">' + (task.state === "run" ? pct + "%" : "") + "</span></div>" +
        '<div class="bar"><i style="width:' + pct + '%"></i></div>' +
        '<div class="task-sub">' + esc(sub) + "</div>" +
        "</div>";
    }
    var scroll = $sheetBody.scrollTop;
    $sheetBody.innerHTML = html;
    if ($sheetBody.scrollTop !== scroll) $sheetBody.scrollTop = scroll;

    // While hidden, keep a way back: the pill is the only route to progress
    // and to the cancel button once the panel is gone.
    var activeNow = 0;
    for (var p = 0; p < tasks.length; p++) {
      if (tasks[p].state === "run" || tasks[p].state === "wait") activeNow++;
    }
    $upPill.hidden = !sheetHidden || activeNow === 0;
    if (!$upPill.hidden) $upPillText.textContent = t("activeUploads", [done, tasks.length]);
    if (sheetHidden) return;
    $sheet.classList.add("show");
  }

  // A tab that was open before the binary was rebuilt is running a script that
  // no longer matches the server. It cannot be fixed from the server side, so
  // it asks periodically and reloads itself — never mid-transfer, though.
  function checkBuild(d) {
    if (!d || !d.build || !PAGE_BUILD) return;
    if (d.build === PAGE_BUILD) return;
    if (uploading) return;
    location.reload();
  }

  setInterval(function () {
    if (uploading || document.hidden) return;
    api("/api/info", 5000).then(checkBuild).catch(function () { /* offline */ });
  }, 60000);

  // Bootstrap. The page is already complete; all that is left is replaying a
  // confirmation that was raised just before a reload.
  try {
    var pendingToast = sessionStorage.getItem("landrop.toast");
    if (pendingToast) {
      sessionStorage.removeItem("landrop.toast");
      toast(pendingToast);
    }
  } catch (e) { /* private mode */ }
})();
