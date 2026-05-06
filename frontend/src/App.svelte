<script>
  import { onMount, onDestroy } from 'svelte';
  import { EventsOn, OnFileDrop } from '../wailsjs/runtime/runtime.js';
  import {
    SelectVideo, GetVideoInfo, GeneratePanels, CancelGeneration,
    SelectOutputDir, OpenOutputFolder, GetFFmpegStatus, SavePanelsTo,
    LoadSettings, SaveSettings, DownloadFFmpeg, BrowseForFFmpeg
  } from '../wailsjs/go/main/App.js';

  // ── refs ───────────────────────────────────────────────
  let videoEl;

  // ── state ──────────────────────────────────────────────
  let ffmpegPath  = '';
  let videoPath   = '';
  let videoInfo   = null;
  let outputDir   = '';

  let startTime   = 0;
  let endTime     = 4;

  let fps         = 24;
  let borderWidth = 1;
  let borderColor = '#ffffff';
  let colors      = 64;       // 8–256 palette entries
  let dither      = 'sierra2'; // none | bayer | sierra2 | floyd
  let bayerScale  = 3;        // 0–5, only for bayer
  let scale       = 1.0;      // 0.10–1.0
  let monochrome  = false;

  let dragging      = false;
  let processing    = false;
  let cancelling    = false;
  let progress      = 0;
  let result        = null;
  let error         = '';
  let estMB         = 0;

  // ffmpeg setup state
  let downloading   = false;
  let dlPct         = 0;
  let dlMB          = 0;
  let dlTotal       = 0;
  let dlStatus      = '';
  let dlError       = '';

  const ditherHints = {
    none:    'No dither — smallest file, banding on gradients',
    bayer:   'Ordered pattern — fast, predictable noise, GIF-friendly',
    sierra2: 'Error diffusion — balanced quality and file size',
    floyd:   'Full error diffusion — smoothest gradients, slightly larger file',
  };

  // ── init ───────────────────────────────────────────────
  onMount(async () => {
    ffmpegPath = await GetFFmpegStatus();
    const unsub  = EventsOn('progress',   (d) => { progress = d.panel; });
    const unsub2 = EventsOn('ffmpeg-dl',  (d) => {
      if (d.status) dlStatus = d.status;
      dlPct   = d.pct   ?? dlPct;
      dlMB    = d.mb    ?? dlMB;
      dlTotal = d.total ?? dlTotal;
    });
    onDestroy(() => { unsub(); unsub2(); });

    // Wails native file drop — receives absolute paths on Windows
    OnFileDrop((_x, _y, paths) => {
      if (paths[0]) applyVideo(paths[0]);
    }, false);

    // Restore last-used settings
    try {
      const s = await LoadSettings();
      fps         = s.fps         || 24;
      borderWidth = s.borderWidth ?? 1;
      borderColor = s.borderColor || '#ffffff';
      colors      = s.colors      || 64;
      dither      = s.dither      || 'sierra2';
      bayerScale  = s.bayerScale  ?? 3;
      scale       = s.scale       || 1.0;
      monochrome  = !!s.monochrome;
      if (s.outputDir) outputDir = s.outputDir;
    } catch (_) {}
  });

  // ── video pick ─────────────────────────────────────────
  async function applyVideo(path) {
    videoPath = path;
    result    = null;
    error     = '';
    try {
      videoInfo = await GetVideoInfo(path);
      startTime = 0;
      endTime   = Math.min(4, parseFloat(videoInfo.duration.toFixed(2)));
    } catch (e) { error = e.toString(); }
  }

  async function pickVideo() {
    const path = await SelectVideo();
    if (path) applyVideo(path);
  }

  // ── video loop in selected range ───────────────────────
  function onVideoLoaded() {
    if (!videoEl) return;
    videoEl.currentTime = startTime;
    videoEl.play().catch(() => {});
  }

  function handleTimeUpdate() {
    if (!videoEl) return;
    // loop back to start when we hit the end marker (with 80ms buffer)
    if (videoEl.currentTime >= endTime - 0.08) {
      videoEl.currentTime = startTime;
      videoEl.play().catch(() => {});
    }
  }

  // ── range slider handlers ──────────────────────────────
  function onStartInput(e) {
    const v = parseFloat(e.target.value);
    if (v >= endTime - 0.1) return;
    startTime = v;
    if (videoEl) {
      videoEl.currentTime = startTime;
      videoEl.play().catch(() => {});
    }
  }

  function onEndInput(e) {
    const v = parseFloat(e.target.value);
    if (v <= startTime + 0.1) return;
    endTime = v;
  }

  // ── output dir ─────────────────────────────────────────
  async function pickOutputDir() {
    const path = await SelectOutputDir();
    if (path) outputDir = path;
  }

  // ── generate ───────────────────────────────────────────
  async function generate() {
    if (!videoPath) return;
    SaveSettings({ fps, borderWidth, borderColor, colors, dither, bayerScale, scale, monochrome, outputDir })
      .catch(() => {});
    processing = true;
    cancelling = false;
    progress   = 0;
    result     = null;
    error      = '';
    try {
      result = await GeneratePanels({
        videoPath, startTime, endTime, fps, borderWidth, borderColor,
        colors, dither, bayerScale, scale, monochrome
      });
    } catch (e) {
      if (!cancelling) error = e.toString();
    }
    processing = false;
    cancelling = false;
  }

  async function cancelGeneration() {
    cancelling = true;
    await CancelGeneration();
  }

  // ── ffmpeg setup ───────────────────────────────────────
  async function downloadFFmpeg() {
    downloading = true; dlPct = 0; dlMB = 0; dlStatus = 'locating'; dlError = '';
    try {
      await DownloadFFmpeg();
      ffmpegPath = await GetFFmpegStatus();
    } catch (e) { dlError = e.toString(); }
    downloading = false;
  }

  async function browseForFFmpeg() {
    dlError = '';
    try {
      const p = await BrowseForFFmpeg();
      if (p) ffmpegPath = p;
    } catch (e) { dlError = e.toString(); }
  }

  // ── helpers ────────────────────────────────────────────
  function fmt(s) {
    const m = Math.floor(s / 60);
    const sec = (s % 60).toFixed(1).padStart(4, '0');
    return m > 0 ? `${m}:${sec}` : `${sec}s`;
  }

  function sizeColor(mb) {
    if (mb > 5)  return 'var(--error)';
    if (mb > 4)  return 'var(--warn)';
    return 'var(--success)';
  }

  // Serve local video through Wails asset server — WebView2 blocks file:/// directly
  $: videoSrc = videoPath ? `/localfile?path=${encodeURIComponent(videoPath)}` : '';

  $: dur       = endTime - startTime;
  $: maxTime   = videoInfo ? videoInfo.duration : 30;
  $: startPct  = videoInfo ? (startTime / maxTime) * 100 : 0;
  $: endPct    = videoInfo ? (endTime   / maxTime) * 100 : 40;
  $: progressPct = processing ? Math.round((progress / 5) * 100) : 0;

  // rough GIF size estimate — uses capped fps so it matches what the backend actually encodes
  $: estMB = (() => {
    if (!videoInfo) return 0;
    const cfMap = { none: 0.07, bayer: 0.11, sierra2: 0.13, floyd: 0.15 };
    const cf = cfMap[dither] ?? 0.13;
    const oW = Math.max(1, Math.floor(Math.floor(videoInfo.width / 5) * scale));
    const oH = Math.max(1, Math.floor(videoInfo.height * scale));
    const srcFPS = videoInfo.fps ? Math.round(videoInfo.fps) : 0;
    const effFPS = srcFPS > 0 ? Math.min(fps, srcFPS, 50) : Math.min(fps, 50);
    const fr = Math.max(1, Math.round(dur * effFPS));
    return (oW * oH * fr * (colors / 256) * cf * (monochrome ? 0.6 : 1)) / (1024 * 1024);
  })();

  // raise start thumb above end thumb when they are close or overlapping
  $: zStart = (startPct >= endPct - 3 || startPct > 80) ? 5 : 3;
</script>

<!-- ═══════════════════════════════════════════════════ -->
<div class="shell">

  <!-- header -->
  <header>
    <span class="logo">🎮 Steam Showcase Maker</span>
    {#if ffmpegPath}
      <span class="badge ok">ffmpeg ready</span>
    {:else}
      <span class="badge warn">ffmpeg not found</span>
    {/if}
  </header>

  <div class="body">

    <!-- ── SIDEBAR ───────────────────────────────────── -->
    <aside>

      <!-- video section -->
      <section>
        <h2>Video</h2>

        <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
        <div class="dropzone"
             on:click={!videoPath ? pickVideo : null}
             on:dragover|preventDefault={() => dragging = true}
             on:dragleave={() => dragging = false}
             class:has-video={!!videoPath}
             class:drag-over={dragging}>
          {#if videoPath}
            <video
              bind:this={videoEl}
              src={videoSrc}
              on:loadedmetadata={onVideoLoaded}
              on:timeupdate={handleTimeUpdate}
              muted autoplay playsinline
            ></video>
          {:else}
            <div class="drop-hint">
              <div class="drop-icon">▶</div>
              <p>Click to select video</p>
              <p class="sub">mp4 · mov · avi · mkv · gif</p>
            </div>
          {/if}
        </div>

        {#if videoPath}
          <button class="btn-change" on:click={pickVideo}>Change video</button>
        {/if}

        {#if videoInfo}
          {@const cropW = Math.floor(videoInfo.width / 5)}
          {@const cropH = videoInfo.height}
          {@const outW = Math.floor(cropW * scale)}
          {@const outH = Math.floor(cropH * scale)}
          {@const srcFPS = videoInfo.fps ? Math.round(videoInfo.fps) : 0}
          {@const effFPS = srcFPS > 0 ? Math.min(fps, srcFPS, 50) : Math.min(fps, 50)}
          {@const frames = Math.round(effFPS * dur)}
          <div class="meta-block">
            <div class="meta-row">
              <span class="meta-label">Source</span>
              <span>{videoInfo.width}×{videoInfo.height} · {videoInfo.duration.toFixed(1)}s · {videoInfo.fileSize.toFixed(2)} MB{srcFPS ? ` · ${srcFPS}fps` : ''}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Panel</span>
              <span>{cropW}×{cropH}{scale < 1 ? ` → ${outW}×${outH}` : ''}{monochrome ? ' · gray' : ''}</span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Frames</span>
              <span>
                {frames} @ {effFPS}fps · {dur.toFixed(1)}s
                {#if fps > effFPS}
                  <span class="fps-cap"> (capped from {fps})</span>
                {/if}
              </span>
            </div>
            <div class="meta-row">
              <span class="meta-label">Est. size</span>
              <span style="color:{estMB > 5 ? 'var(--error)' : estMB > 4 ? 'var(--warn)' : 'var(--accent2)'}">
                ~{estMB.toFixed(1)} MB/panel
              </span>
            </div>
          </div>
        {/if}
      </section>

      <!-- trim range -->
      {#if videoInfo}
      <section>
        <h2>Trim</h2>

        <!-- dual range slider -->
        <div class="range-slider">
          <div class="track">
            <div class="fill" style="left:{startPct}%; right:{100 - endPct}%"></div>
          </div>
          <input class="thumb" style="z-index:{zStart}"
            type="range" min="0" max={maxTime} step="0.05"
            value={startTime} on:input={onStartInput} />
          <input class="thumb" style="z-index:4"
            type="range" min="0" max={maxTime} step="0.05"
            value={endTime} on:input={onEndInput} />
        </div>

        <div class="time-labels">
          <span class="time-tag start">▶ {fmt(startTime)}</span>
          <span class="clip-dur">{fmt(dur)}</span>
          <span class="time-tag end">{fmt(endTime)} ■</span>
        </div>
      </section>
      {/if}

      <!-- settings -->
      <section>
        <h2>Settings</h2>

        <div class="field">
          <label>FPS <span class="val">{fps}</span></label>
          <input type="range" min="5" max="60" step="1" bind:value={fps} />
        </div>

        <div class="field">
          <label>Border <span class="val">{borderWidth}px</span></label>
          <input type="range" min="0" max="6" step="1" bind:value={borderWidth} />
        </div>

        <div class="field row">
          <label>Color</label>
          <input type="color" bind:value={borderColor} />
          <span class="val">{borderColor}</span>
        </div>

        <!-- colors -->
        <div class="field">
          <label>Colors <span class="val">{colors}</span></label>
          <input type="range" min="8" max="256" step="8" bind:value={colors} />
          <div class="q-labels"><span>Smaller</span><span>Better</span></div>
        </div>

        <!-- dither -->
        <div class="field">
          <label>Dither</label>
          <select bind:value={dither}>
            <option value="none">None (smallest file)</option>
            <option value="bayer">Bayer (fast, low noise)</option>
            <option value="sierra2">Sierra2 (balanced)</option>
            <option value="floyd">Floyd-Steinberg (best)</option>
          </select>
          <p class="hint-text">{ditherHints[dither]}</p>
        </div>

        <!-- bayer scale (only when bayer selected) -->
        {#if dither === 'bayer'}
        <div class="field">
          <label>Bayer Scale <span class="val">{bayerScale}</span></label>
          <input type="range" min="0" max="5" step="1" bind:value={bayerScale} />
          <div class="q-labels"><span>Fine</span><span>Coarse</span></div>
        </div>
        {/if}

        <!-- rescale -->
        <div class="field">
          <label>Rescale <span class="val">{Math.round(scale * 100)}%</span></label>
          <input type="range" min="0.10" max="1" step="0.05" bind:value={scale} />
          <div class="q-labels">
            <span>Smaller</span>
            <span>Native</span>
          </div>
        </div>

        <!-- monochrome -->
        <div class="field row">
          <label>Monochrome</label>
          <input type="checkbox" bind:checked={monochrome} />
        </div>

        <div class="field row">
          <label>Output</label>
          <button class="btn-sm" on:click={pickOutputDir}>Browse</button>
        </div>
        {#if outputDir}
          <p class="path-note">{outputDir}</p>
        {:else}
          <p class="path-note dim">Temp folder</p>
        {/if}
      </section>

      <!-- actions -->
      <div class="actions">
        {#if !ffmpegPath}
          <!-- ── ffmpeg setup ──────────────────────────── -->
          <div class="setup-box">
            <p class="setup-title">ffmpeg required</p>
            <p class="setup-sub">ffmpeg is the only external tool this app needs.
              It handles all video decoding and GIF encoding.</p>

            {#if downloading}
              <div class="dl-row">
                {#if dlStatus === 'locating'}
                  <span class="dl-label">Locating latest build…</span>
                {:else if dlStatus === 'extracting'}
                  <span class="dl-label">Extracting…</span>
                {:else}
                  <div class="prog-bar">
                    <div style="width:{dlPct.toFixed(0)}%"></div>
                  </div>
                  <span class="dl-label">
                    {dlPct.toFixed(0)}%{dlTotal > 0 ? ` · ${dlMB.toFixed(0)}/${dlTotal.toFixed(0)} MB` : ''}
                  </span>
                {/if}
              </div>
            {:else}
              <button class="btn-primary" on:click={downloadFFmpeg}>
                Download automatically (~35 MB)
              </button>
              <button class="btn-secondary" on:click={browseForFFmpeg}>
                Browse for ffmpeg.exe
              </button>
            {/if}
            {#if dlError}<p class="error-msg">{dlError}</p>{/if}
          </div>
        {:else}
          <button class="btn-primary" on:click={generate}
            disabled={!videoPath || processing}>
            {#if processing}
              Processing… {progressPct}%
            {:else}
              Generate Panels
            {/if}
          </button>
          {#if processing}
            <button class="btn-cancel" on:click={cancelGeneration} disabled={cancelling}>
              {cancelling ? 'Cancelling…' : 'Cancel'}
            </button>
          {/if}
          {#if result}
            <button class="btn-secondary" on:click={OpenOutputFolder}>
              Open Output Folder
            </button>
            <button class="btn-secondary" on:click={() => SavePanelsTo(result.files)}>
              Save to…
            </button>
          {/if}
        {/if}
      </div>

      {#if error}
        <p class="error-msg">{error}</p>
      {/if}

    </aside>

    <!-- ── MAIN PREVIEW ──────────────────────────────── -->
    <main>

      {#if processing}
        <div class="center-msg">
          <div class="spinner"></div>
          <p>{progress > 0 ? `Processing panel ${progress} of 5…` : 'Preparing…'}</p>
          <div class="prog-bar"><div style="width:{progressPct}%"></div></div>
        </div>

      {:else if result}
        <div class="preview-area">
          <h2>Preview <span class="dim">— Steam Workshop Showcase</span></h2>

          <div class="steam-mockup">
            <div class="showcase-label">WORKSHOP SHOWCASE</div>
            <div class="panels-row">
              {#each result.panels as src, i}
                <div class="panel-wrap">
                  <img {src} alt="Panel {i + 1}" />
                  <div class="panel-badge" style="color:{sizeColor(result.sizes[i])}">
                    {result.sizes[i].toFixed(1)} MB{result.sizes[i] > 5 ? ' ⚠' : ''}
                  </div>
                </div>
              {/each}
            </div>
          </div>

          {#if result.sizes.some(s => s > 5)}
            <p class="hint warn">⚠ Some panels exceed 5 MB — try lower quality or shorter clip.</p>
          {:else}
            <p class="hint ok">✓ All panels within Steam's 5 MB limit.</p>
          {/if}
        </div>

      {:else if videoPath}
        <div class="center-msg">
          <p class="dim">Adjust settings then click <strong>Generate Panels</strong></p>
        </div>

      {:else}
        <div class="center-msg">
          <div class="placeholder-panels">
            {#each Array(5) as _, i}
              <div class="placeholder-panel">{i + 1}</div>
            {/each}
          </div>
          <p class="dim">Select a video to get started</p>
        </div>
      {/if}

    </main>
  </div>
</div>

<!-- ═══════════════════════════════════════════════════ -->
<style>
  .shell {
    display: flex; flex-direction: column; height: 100vh; background: var(--bg);
  }

  /* header */
  header {
    display: flex; align-items: center; gap: 12px;
    padding: 10px 16px;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
    flex-shrink: 0;
  }
  .logo { font-weight: bold; font-size: 15px; color: var(--accent2); }
  .badge { font-size: 12px; padding: 2px 8px; border-radius: 10px; }
  .badge.ok   { background: #1a4a3a; color: var(--success); }
  .badge.warn { background: #4a2a1a; color: var(--warn); }

  /* layout */
  .body { display: flex; flex: 1; overflow: hidden; }

  /* sidebar */
  aside {
    width: 300px; flex-shrink: 0;
    background: var(--bg3);
    border-right: 1px solid var(--border);
    display: flex; flex-direction: column;
    overflow-y: auto;
    padding: 12px; gap: 14px;
  }

  section { display: flex; flex-direction: column; gap: 8px; }
  h2 {
    font-size: 10px; text-transform: uppercase; letter-spacing: 0.1em;
    color: var(--text-dim);
    padding-bottom: 4px; border-bottom: 1px solid var(--border);
  }

  /* dropzone */
  .dropzone {
    background: var(--surface);
    border: 1px dashed var(--border);
    border-radius: var(--radius);
    overflow: hidden;
    min-height: 100px;
    display: flex; align-items: center; justify-content: center;
    transition: border-color 0.2s;
    cursor: pointer;
  }
  .dropzone.has-video { cursor: default; }
  .dropzone:not(.has-video):hover { border-color: var(--accent); }
  .dropzone.drag-over {
    border-color: var(--accent); border-style: solid;
    box-shadow: 0 0 0 3px rgba(26, 159, 255, 0.25);
  }
  .dropzone video { width: 100%; display: block; max-height: 160px; object-fit: contain; background: #000; }
  .drop-hint { text-align: center; padding: 16px; color: var(--text-dim); }
  .drop-icon { font-size: 26px; margin-bottom: 6px; color: var(--accent); }
  .sub { font-size: 11px; margin-top: 4px; }

  .btn-change {
    background: transparent; border: 1px solid var(--border);
    color: var(--text-dim); padding: 3px 8px; font-size: 11px;
    align-self: flex-start; border-radius: var(--radius);
  }
  .meta-block {
    display: flex; flex-direction: column; gap: 3px;
    background: var(--surface); border-radius: var(--radius);
    padding: 6px 8px;
  }
  .meta-row {
    display: flex; justify-content: space-between; align-items: baseline;
    font-size: 12px; gap: 6px;
  }
  .meta-label { color: var(--text-dim); flex-shrink: 0; }
  .meta-row span:last-child { color: var(--accent2); text-align: right; }
  .fps-cap { color: var(--warn); font-size: 10px; }

  /* ── dual range slider ─────────────────────────────── */
  .range-slider {
    position: relative; height: 24px;
    display: flex; align-items: center;
    margin: 4px 0;
  }
  .track {
    position: absolute; left: 0; right: 0;
    height: 4px; background: var(--bg2); border-radius: 2px; pointer-events: none;
  }
  .fill {
    position: absolute; height: 100%;
    background: var(--accent); border-radius: 2px;
  }
  .thumb {
    position: absolute; width: 100%;
    height: 24px; background: transparent;
    pointer-events: none;
    -webkit-appearance: none; margin: 0; outline: none;
  }
  .thumb::-webkit-slider-thumb {
    -webkit-appearance: none;
    pointer-events: all;
    width: 18px; height: 18px;
    border-radius: 50%;
    background: var(--accent);
    border: 2px solid var(--bg3);
    cursor: grab;
    box-shadow: 0 1px 4px rgba(0,0,0,0.5);
  }
  .thumb::-webkit-slider-thumb:hover { background: var(--accent2); }
  .thumb::-webkit-slider-thumb:active { cursor: grabbing; }

  .time-labels {
    display: flex; justify-content: space-between; align-items: center;
    font-size: 12px;
  }
  .time-tag { color: var(--accent2); font-weight: bold; }
  .clip-dur { color: var(--text-dim); font-size: 11px; }

  /* settings fields */
  .field { display: flex; flex-direction: column; gap: 4px; }
  .field.row { flex-direction: row; align-items: center; gap: 8px; }
  label {
    font-size: 13px; color: var(--text-dim);
    display: flex; justify-content: space-between;
  }
  .val { color: var(--accent2); font-weight: bold; }

  .q-labels {
    display: flex; justify-content: space-between;
    font-size: 11px; color: var(--text-dim); margin-top: 1px;
  }

  .path-note { font-size: 11px; color: var(--text-dim); word-break: break-all; }
  .path-note.dim { font-style: italic; }
  .hint-text { font-size: 11px; color: var(--text-dim); margin-top: 2px; }

  /* buttons */
  .btn-primary {
    width: 100%; padding: 10px;
    background: var(--accent); color: #fff;
    font-weight: bold; font-size: 13px;
  }
  .btn-secondary {
    width: 100%; padding: 8px;
    background: var(--bg2); color: var(--text);
    border: 1px solid var(--border);
  }
  .btn-cancel {
    width: 100%; padding: 8px;
    background: #2a1010; color: var(--error);
    border: 1px solid #5a2020;
  }
  .btn-cancel:disabled { opacity: 0.5; cursor: default; }

  .setup-box {
    display: flex; flex-direction: column; gap: 8px;
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 12px;
  }
  .setup-title { font-size: 13px; font-weight: bold; color: var(--warn); }
  .setup-sub   { font-size: 11px; color: var(--text-dim); line-height: 1.4; }
  .dl-row { display: flex; flex-direction: column; gap: 4px; }
  .dl-label { font-size: 11px; color: var(--text-dim); text-align: center; }
  .btn-sm {
    padding: 3px 10px;
    background: var(--bg2); color: var(--text);
    border: 1px solid var(--border); white-space: nowrap;
  }
  .actions { display: flex; flex-direction: column; gap: 6px; margin-top: auto; }
  .error-msg { color: var(--error); font-size: 11px; word-break: break-all; }

  /* main */
  main {
    flex: 1; display: flex;
    align-items: center; justify-content: center;
    overflow: auto; padding: 24px;
  }
  .center-msg {
    text-align: center; display: flex; flex-direction: column;
    align-items: center; gap: 16px; color: var(--text-dim);
  }

  /* placeholder */
  .placeholder-panels { display: flex; gap: 4px; margin-bottom: 12px; }
  .placeholder-panel {
    width: 60px; height: 160px;
    background: var(--bg2); border: 1px dashed var(--border); border-radius: 3px;
    display: flex; align-items: center; justify-content: center;
    color: var(--text-dim); font-size: 18px;
  }

  /* spinner + progress */
  .spinner {
    width: 32px; height: 32px;
    border: 3px solid var(--bg2); border-top-color: var(--accent);
    border-radius: 50%; animation: spin 0.8s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  .prog-bar {
    width: 240px; height: 6px; background: var(--bg2);
    border-radius: 3px; overflow: hidden;
  }
  .prog-bar div {
    height: 100%; background: var(--accent);
    border-radius: 3px; transition: width 0.3s;
  }

  /* preview */
  .preview-area {
    display: flex; flex-direction: column;
    gap: 16px; align-items: center; width: 100%;
  }
  .preview-area h2 { font-size: 14px; }
  .dim { color: var(--text-dim); }

  .steam-mockup {
    background: var(--surface); border: 1px solid var(--border);
    border-radius: var(--radius); padding: 12px;
    width: 100%; max-width: 720px;
  }
  .showcase-label {
    font-size: 10px; text-transform: uppercase; letter-spacing: 0.1em;
    color: var(--text-dim); margin-bottom: 8px;
  }
  .panels-row { display: flex; gap: 3px; justify-content: center; }
  .panel-wrap { display: flex; flex-direction: column; align-items: center; gap: 4px; flex: 1; }
  .panel-wrap img { width: 100%; aspect-ratio: auto; object-fit: cover; border-radius: 2px; display: block; }
  .panel-badge { font-size: 10px; font-weight: bold; }

  .hint { font-size: 12px; padding: 6px 12px; border-radius: var(--radius); }
  .hint.warn { background: #3a2010; color: var(--warn); }
  .hint.ok   { background: #102a20; color: var(--success); }
</style>
