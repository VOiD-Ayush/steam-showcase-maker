package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// exec kept for LookPath in findFFmpeg

type App struct {
	ctx         context.Context
	ffmpegPath  string
	ffprobePath string
	outputDir   string
	tempDir     string
	genMu       sync.Mutex
	cancelGen   context.CancelFunc
}

func (a *App) CancelGeneration() {
	a.genMu.Lock()
	fn := a.cancelGen
	a.genMu.Unlock()
	if fn != nil {
		fn()
	}
}

type VideoInfo struct {
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Duration float64 `json:"duration"`
	FileSize float64 `json:"fileSize"` // MB
	FPS      float64 `json:"fps"`
	Path     string  `json:"path"`
}

// parseFrameRate parses ffprobe's "num/den" frame-rate string.
func parseFrameRate(s string) float64 {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0
	}
	num, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	den, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil || den == 0 {
		return 0
	}
	return num / den
}

type ProcessOptions struct {
	VideoPath   string  `json:"videoPath"`
	StartTime   float64 `json:"startTime"`
	EndTime     float64 `json:"endTime"`
	FPS         int     `json:"fps"`
	BorderWidth int     `json:"borderWidth"`
	BorderColor string  `json:"borderColor"`
	Colors      int     `json:"colors"`     // 2–256 palette entries
	Dither      string  `json:"dither"`     // "none"|"bayer"|"sierra2"|"floyd"
	BayerScale  int     `json:"bayerScale"` // 0–5, only used with dither=bayer
	Scale       float64 `json:"scale"`      // 0.10–1.0; 1.0 = native size
	Monochrome  bool    `json:"monochrome"` // strip colour, use gray palette
}

type ProcessResult struct {
	Panels []string  `json:"panels"` // base64 data URLs for preview
	Files  []string  `json:"files"`  // absolute output paths
	Sizes  []float64 `json:"sizes"`  // MB per panel
}

type Settings struct {
	FPS         int     `json:"fps"`
	BorderWidth int     `json:"borderWidth"`
	BorderColor string  `json:"borderColor"`
	Colors      int     `json:"colors"`
	Dither      string  `json:"dither"`
	BayerScale  int     `json:"bayerScale"`
	Scale       float64 `json:"scale"`
	Monochrome  bool    `json:"monochrome"`
	OutputDir   string  `json:"outputDir"`
}

func defaultSettings() Settings {
	return Settings{
		FPS: 24, BorderWidth: 1, BorderColor: "#ffffff",
		Colors: 64, Dither: "sierra2", BayerScale: 3, Scale: 1.0,
	}
}

func (a *App) settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = a.tempDir
	}
	return filepath.Join(dir, "steam-showcase-maker", "settings.json")
}

func (a *App) LoadSettings() Settings {
	data, err := os.ReadFile(a.settingsPath())
	if err != nil {
		return defaultSettings()
	}
	s := defaultSettings()
	if err := json.Unmarshal(data, &s); err != nil {
		return defaultSettings()
	}
	if s.OutputDir != "" {
		a.outputDir = s.OutputDir
	}
	return s
}

func (a *App) SaveSettings(s Settings) error {
	path := a.settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.ffmpegPath, a.ffprobePath = findFFmpeg()
	tmp, err := os.MkdirTemp("", "steam-showcase-*")
	if err == nil {
		a.tempDir = tmp
	}
}

func (a *App) shutdown(ctx context.Context) {
	if a.tempDir != "" {
		os.RemoveAll(a.tempDir)
	}
}

// ffmpegBinDir returns the directory where the app caches downloaded ffmpeg binaries.
func ffmpegBinDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "steam-showcase-maker", "bin")
	}
	return ""
}

func findFFmpeg() (ffmpeg, ffprobe string) {
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		probe, _ := exec.LookPath("ffprobe")
		return path, probe
	}
	// Check app's own downloaded cache first.
	if binDir := ffmpegBinDir(); binDir != "" {
		ff := filepath.Join(binDir, "ffmpeg.exe")
		if _, err := os.Stat(ff); err == nil {
			fp := filepath.Join(binDir, "ffprobe.exe")
			if _, err2 := os.Stat(fp); err2 != nil {
				fp = ""
			}
			return ff, fp
		}
	}
	candidates := []string{
		`C:\Program Files\ffmpeg\bin`,
		`C:\Program Files (x86)\ffmpeg\bin`,
		`C:\ffmpeg\bin`,
	}
	for _, dir := range candidates {
		ff := filepath.Join(dir, "ffmpeg.exe")
		if _, err := os.Stat(ff); err == nil {
			fp := filepath.Join(dir, "ffprobe.exe")
			if _, err2 := os.Stat(fp); err2 != nil {
				fp = ""
			}
			return ff, fp
		}
	}
	return "", ""
}

func (a *App) GetFFmpegStatus() string {
	if a.ffmpegPath == "" {
		return ""
	}
	return a.ffmpegPath
}

// BrowseForFFmpeg opens a file picker so the user can point to their own ffmpeg.exe.
// ffprobe.exe is expected in the same directory.
func (a *App) BrowseForFFmpeg() (string, error) {
	path, err := wailsrt.OpenFileDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Select ffmpeg.exe",
		Filters: []wailsrt.FileFilter{
			{DisplayName: "ffmpeg executable", Pattern: "ffmpeg.exe;ffmpeg"},
		},
	})
	if err != nil || path == "" {
		return "", err
	}
	dir := filepath.Dir(path)
	probe := filepath.Join(dir, "ffprobe.exe")
	if _, err2 := os.Stat(probe); err2 != nil {
		probe = ""
	}
	a.ffmpegPath = path
	a.ffprobePath = probe
	return path, nil
}

// btbnZipURL queries the GitHub API for the latest BtbN release and returns
// the browser_download_url for the win64-lgpl-essentials ZIP.
func (a *App) btbnZipURL() (string, error) {
	const api = "https://api.github.com/repos/BtbN/FFmpeg-Builds/releases/latest"
	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, api, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "steam-showcase-maker")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub API unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("parse API response: %w", err)
	}
	// Pick win64 LGPL static (non-shared) zip.
	// Prefer the latest stable release (ffmpeg-nX.Y-…) over the rolling master build.
	// Iterating in asset order means the last stable match is the highest version.
	var stable, master string
	for _, a := range release.Assets {
		n := strings.ToLower(a.Name)
		if strings.Contains(n, "win64") && strings.Contains(n, "lgpl") &&
			!strings.Contains(n, "shared") && strings.HasSuffix(n, ".zip") {
			if strings.Contains(n, "master") {
				master = a.BrowserDownloadURL
			} else {
				stable = a.BrowserDownloadURL // last stable entry = highest version
			}
		}
	}
	if stable != "" {
		return stable, nil
	}
	if master != "" {
		return master, nil
	}
	return "", fmt.Errorf("no compatible win64-lgpl build found in latest BtbN release")
}

// DownloadFFmpeg fetches a minimal static build from BtbN and caches it in
// %APPDATA%\steam-showcase-maker\bin\.  Progress is emitted as "ffmpeg-dl" events:
//   { pct: 0–100, mb: downloaded, total: totalMB, status: "locating"|"downloading"|"extracting"|"done" }
func (a *App) DownloadFFmpeg() error {
	binDir := ffmpegBinDir()
	if binDir == "" {
		return fmt.Errorf("cannot determine app data directory")
	}
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	// ── resolve current download URL via GitHub API ───────────────────────
	wailsrt.EventsEmit(a.ctx, "ffmpeg-dl", map[string]interface{}{
		"status": "locating", "pct": 0.0,
	})
	zipURL, err := a.btbnZipURL()
	if err != nil {
		return err
	}

	// ── download ──────────────────────────────────────────────────────────
	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, zipURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "steam-showcase-maker")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "zip") && !strings.Contains(ct, "octet-stream") && ct != "" {
		return fmt.Errorf("unexpected content type %q — is GitHub reachable?", ct)
	}

	totalBytes := resp.ContentLength
	totalMB := float64(totalBytes) / 1024 / 1024

	tmp, err := os.CreateTemp("", "ffmpeg-dl-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	var downloaded int64
	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := tmp.Write(buf[:n]); werr != nil {
				tmp.Close()
				return werr
			}
			downloaded += int64(n)
			pct := 0.0
			if totalBytes > 0 {
				pct = float64(downloaded) / float64(totalBytes) * 100
			}
			wailsrt.EventsEmit(a.ctx, "ffmpeg-dl", map[string]interface{}{
				"status": "downloading",
				"pct":    pct,
				"mb":     float64(downloaded) / 1024 / 1024,
				"total":  totalMB,
			})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			tmp.Close()
			return fmt.Errorf("download error: %w", readErr)
		}
	}
	tmp.Close()

	// ── extract ffmpeg.exe + ffprobe.exe ──────────────────────────────────
	wailsrt.EventsEmit(a.ctx, "ffmpeg-dl", map[string]interface{}{
		"status": "extracting", "pct": 100.0,
	})

	zr, err := zip.OpenReader(tmpPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	want := map[string]string{
		"ffmpeg.exe":  filepath.Join(binDir, "ffmpeg.exe"),
		"ffprobe.exe": filepath.Join(binDir, "ffprobe.exe"),
	}
	for _, f := range zr.File {
		dest, ok := want[filepath.Base(f.Name)]
		if !ok || f.FileInfo().IsDir() {
			continue
		}
		if err := extractZipEntry(f, dest); err != nil {
			return fmt.Errorf("extract %s: %w", filepath.Base(f.Name), err)
		}
		delete(want, filepath.Base(f.Name))
	}
	if len(want) > 0 {
		return fmt.Errorf("archive did not contain the expected ffmpeg binaries")
	}

	a.ffmpegPath = filepath.Join(binDir, "ffmpeg.exe")
	a.ffprobePath = filepath.Join(binDir, "ffprobe.exe")

	wailsrt.EventsEmit(a.ctx, "ffmpeg-dl", map[string]interface{}{
		"status": "done", "pct": 100.0,
	})
	return nil
}

func extractZipEntry(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

func (a *App) SelectVideo() (string, error) {
	path, err := wailsrt.OpenFileDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Select Video",
		Filters: []wailsrt.FileFilter{
			{DisplayName: "Video Files", Pattern: "*.mp4;*.mov;*.avi;*.mkv;*.webm;*.gif"},
		},
	})
	return path, err
}

func (a *App) SelectOutputDir() (string, error) {
	path, err := wailsrt.OpenDirectoryDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Choose Output Folder",
	})
	if err == nil && path != "" {
		a.outputDir = path
	}
	return path, err
}

func (a *App) GetVideoInfo(videoPath string) (VideoInfo, error) {
	if a.ffprobePath == "" {
		return VideoInfo{}, fmt.Errorf("ffprobe not found")
	}
	cmd := newCmd(a.ctx, a.ffprobePath,
		"-v", "quiet", "-print_format", "json",
		"-show_streams", "-show_format", videoPath,
	)
	out, err := cmd.Output()
	if err != nil {
		return VideoInfo{}, fmt.Errorf("ffprobe failed: %w", err)
	}
	var data struct {
		Streams []struct {
			CodecType    string `json:"codec_type"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			AvgFrameRate string `json:"avg_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return VideoInfo{}, err
	}
	info := VideoInfo{Path: videoPath}
	for _, s := range data.Streams {
		if s.CodecType == "video" {
			info.Width = s.Width
			info.Height = s.Height
			info.FPS = parseFrameRate(s.AvgFrameRate)
		}
	}
	info.Duration, _ = strconv.ParseFloat(data.Format.Duration, 64)
	if fi, err := os.Stat(videoPath); err == nil {
		info.FileSize = float64(fi.Size()) / 1024 / 1024
	}
	return info, nil
}

func (a *App) GeneratePanels(opts ProcessOptions) (ProcessResult, error) {
	if a.ffmpegPath == "" {
		return ProcessResult{}, fmt.Errorf("ffmpeg not found — install ffmpeg and add it to PATH")
	}

	// Probe native dimensions so we never upscale.
	info, err := a.GetVideoInfo(opts.VideoPath)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("cannot read video dimensions: %w", err)
	}

	const numPanels = 5
	nativePanelW := info.Width / numPanels
	nativePanelH := info.Height

	// Cap fps to the source video's native rate so we never duplicate frames.
	// Also cap at 50 — GIF's 2cs minimum delay makes anything above 50fps
	// produce 50fps output while wasting space on duplicate frames.
	exportFPS := opts.FPS
	if info.FPS > 0 {
		srcFPS := int(math.Round(info.FPS))
		if srcFPS < exportFPS {
			exportFPS = srcFPS
		}
	}
	if exportFPS > 50 {
		exportFPS = 50
	}
	if exportFPS < 1 {
		exportFPS = 1
	}

	scale := opts.Scale
	if scale <= 0 || scale > 1 {
		scale = 1.0
	}

	outDir := a.tempDir
	if a.outputDir != "" {
		if _, err := os.Stat(a.outputDir); err != nil {
			return ProcessResult{}, fmt.Errorf("output folder is missing or inaccessible: %w", err)
		}
		outDir = a.outputDir
	}

	// Cancellable context for this run; CancelGeneration() triggers it.
	genCtx, cancel := context.WithCancel(a.ctx)
	a.genMu.Lock()
	a.cancelGen = cancel
	a.genMu.Unlock()
	defer func() {
		cancel()
		a.genMu.Lock()
		a.cancelGen = nil
		a.genMu.Unlock()
	}()

	result := ProcessResult{}
	dur := opts.EndTime - opts.StartTime

	for i := 0; i < numPanels; i++ {
		wailsrt.EventsEmit(a.ctx, "progress", map[string]interface{}{
			"panel": i + 1,
			"total": numPanels,
		})

		outPath     := filepath.Join(outDir, fmt.Sprintf("panel_%d.gif", i+1))
		palettePath := outPath + ".palette.png"

		// Build filter chain: crop → scale → mono → border → fps
		xOffset := nativePanelW * i
		vf := fmt.Sprintf("crop=%d:%d:%d:0", nativePanelW, nativePanelH, xOffset)
		if scale < 1.0 {
			scaledW := int(float64(nativePanelW) * scale)
			scaledH := int(float64(nativePanelH) * scale)
			vf += fmt.Sprintf(",scale=%d:%d:flags=lanczos", scaledW, scaledH)
		}
		if opts.Monochrome {
			vf += ",format=gray"
		}
		if opts.BorderWidth > 0 {
			color := strings.TrimPrefix(opts.BorderColor, "#")
			vf += fmt.Sprintf(",drawbox=x=0:y=0:w=iw:h=ih:color=%s@1.0:t=%d", color, opts.BorderWidth)
		}
		vf += fmt.Sprintf(",fps=%d", exportFPS)

		// Dither filter string
		ditherFilter := ""
		switch opts.Dither {
		case "none":
			ditherFilter = "paletteuse=dither=none"
		case "bayer":
			bs := opts.BayerScale
			if bs < 0 { bs = 0 }
			if bs > 5 { bs = 5 }
			ditherFilter = fmt.Sprintf("paletteuse=dither=bayer:bayer_scale=%d", bs)
		case "floyd":
			ditherFilter = "paletteuse=dither=floyd_steinberg"
		default: // "sierra2" or empty
			ditherFilter = "paletteuse=dither=sierra2_4a"
		}
		colors := opts.Colors
		if colors < 2   { colors = 2 }
		if colors > 256 { colors = 256 }
		if opts.Monochrome && colors > 32 {
			colors = 32
		}

		// -ss and -t both before -i so they are unambiguously INPUT options for
		// the video file. Placing -t after -i in a multi-input command (Pass 2)
		// would associate it with the palette PNG input instead of the video.
		videoIn := []string{
			"-ss", fmt.Sprintf("%.3f", opts.StartTime),
			"-t",  fmt.Sprintf("%.3f", dur),
			"-i",  opts.VideoPath,
		}

		// Pass 1: palette
		p1 := make([]string, 0, len(videoIn)+5)
		p1  = append(p1, videoIn...)
		p1  = append(p1, "-y", "-vf",
			fmt.Sprintf("%s,palettegen=max_colors=%d:stats_mode=diff", vf, colors),
			palettePath)
		if out, err := newCmd(genCtx, a.ffmpegPath, p1...).CombinedOutput(); err != nil {
			if genCtx.Err() != nil {
				return ProcessResult{}, fmt.Errorf("cancelled")
			}
			return result, fmt.Errorf("panel %d palette failed: %s", i+1, string(out))
		}

		// Pass 2: GIF (+offsetting encodes only changed pixels per frame)
		p2 := make([]string, 0, len(videoIn)+8)
		p2  = append(p2, videoIn...)
		p2  = append(p2, "-y", "-i", palettePath,
			"-lavfi", vf+"[x];[x][1:v]"+ditherFilter,
			"-gifflags", "+offsetting",
			outPath)
		if out, err := newCmd(genCtx, a.ffmpegPath, p2...).CombinedOutput(); err != nil {
			os.Remove(palettePath)
			os.Remove(outPath) // remove partial GIF
			if genCtx.Err() != nil {
				return ProcessResult{}, fmt.Errorf("cancelled")
			}
			return result, fmt.Errorf("panel %d GIF failed: %s", i+1, string(out))
		}
		os.Remove(palettePath)

		// Hex patch: last byte → 0x21 (Steam upload bypass)
		if f, err := os.OpenFile(outPath, os.O_RDWR, 0644); err == nil {
			f.Seek(-1, 2)
			f.Write([]byte{0x21})
			f.Close()
		}

		fi, err := os.Stat(outPath)
		if err != nil {
			return result, err
		}

		// Serve via the local-file middleware — avoids sending a huge base64
		// blob over IPC and lets WebView2 stream the GIF from disk directly.
		result.Panels = append(result.Panels, "/localfile?path="+url.QueryEscape(outPath))
		result.Files  = append(result.Files, outPath)
		result.Sizes  = append(result.Sizes, float64(fi.Size())/1024/1024)
	}

	return result, nil
}

func (a *App) OpenOutputFolder() error {
	dir := a.tempDir
	if a.outputDir != "" {
		dir = a.outputDir
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = newCmd(a.ctx, "explorer", filepath.FromSlash(dir))
	case "darwin":
		cmd = newCmd(a.ctx, "open", dir)
	default:
		cmd = newCmd(a.ctx, "xdg-open", dir)
	}
	return cmd.Start()
}

// SavePanelsTo opens a folder picker then copies all generated panel files there.
func (a *App) SavePanelsTo(files []string) error {
	destDir, err := wailsrt.OpenDirectoryDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Save panels to…",
	})
	if err != nil || destDir == "" {
		return nil
	}
	for _, src := range files {
		dst := filepath.Join(destDir, filepath.Base(src))
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
