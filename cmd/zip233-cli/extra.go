package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/neko233-com/zip233/internal/archive"
)

type benchRow struct {
	Format       string  `json:"format"`
	Original     int64   `json:"original_bytes"`
	Archive      int64   `json:"archive_bytes"`
	Ratio        float64 `json:"ratio"`
	CreateMS     float64 `json:"create_ms"`
	TestMS       float64 `json:"test_ms"`
	ExtractMS    float64 `json:"extract_ms"`
	CreateSpeed  float64 `json:"create_mb_s"`
	TestSpeed    float64 `json:"test_mb_s"`
	ExtractSpeed float64 `json:"extract_mb_s"`
	OK           bool    `json:"ok"`
	Error        string  `json:"error,omitempty"`
}

var benchFormats = []string{"zip", "7z", "tar.gz", "tar.xz", "tar.bz2"}

func benchmarkCmd() *cobra.Command {
	var formats string
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "基准测试创建、校验与解压速度",
		RunE: func(cmd *cobra.Command, args []string) error {
			selected := benchFormats
			if strings.TrimSpace(formats) != "" {
				selected = splitCSV(formats)
			}
			if jsonOutput {
				rows, err := runBenchmark(selected, resolveJobs())
				if err != nil {
					return err
				}
				return json.NewEncoder(os.Stdout).Encode(map[string]any{"rows": rows})
			}
			if useTUI() {
				return runTUI(tuiJob{
					command: "bench",
					target:  strings.Join(selected, " · "),
					work: func() tuiOutcome {
						rows, err := runBenchmark(selected, resolveJobs())
						if err != nil {
							return tuiOutcome{lines: []string{err.Error()}, failed: true, err: err}
						}
						return tuiOutcome{lines: formatBenchRows(rows)}
					},
				})
			}
			rows, err := runBenchmark(selected, resolveJobs())
			if err != nil {
				return err
			}
			for _, line := range formatBenchRows(rows) {
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&formats, "formats", "", "要测试的格式，逗号分隔（默认 zip,7z,tar.gz,tar.xz,tar.bz2）")
	return cmd
}

// defaultJobs is the concurrency cap used when --jobs is not supplied.
// Capping at 8 keeps archive operations from saturating the machine while
// still overlapping I/O-bound create/test/extract work across formats.
func defaultJobs() int {
	n := runtime.NumCPU()
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}

// resolveJobs returns the requested parallelism. A non-positive --jobs value
// means "not set" and falls back to the default.
func resolveJobs() int {
	if flagJobs > 0 {
		return flagJobs
	}
	return defaultJobs()
}

// runBenchmark measures every requested format concurrently. A semaphore
// bounds parallelism to jobs; each goroutine writes its own pre-allocated slot
// under a mutex so the returned rows keep the exact order of formats. Every
// format uses a distinct target/output path, so the workers never collide on
// the filesystem.
func runBenchmark(formats []string, jobs int) ([]benchRow, error) {
	if jobs < 1 {
		jobs = 1
	}
	benchDir, err := os.MkdirTemp("", "zip233-bench-")
	if err != nil {
		return nil, fmt.Errorf("创建基准测试目录失败：%w", err)
	}
	defer os.RemoveAll(benchDir)

	sourceDir := filepath.Join(benchDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建样本目录失败：%w", err)
	}
	sources, original, err := writeBenchSamples(sourceDir)
	if err != nil {
		return nil, err
	}
	// Precompute digests so extraction timing includes a real content check;
	// without it extract_ms was dominated by timer resolution and stayed 0.
	digests, err := hashSources(sources)
	if err != nil {
		return nil, err
	}

	rows := make([]benchRow, len(formats))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, format := range formats {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, format string) {
			defer wg.Done()
			defer func() { <-sem }()
			row := benchOneFormat(benchDir, sources, original, digests, format)
			mu.Lock()
			rows[idx] = row
			mu.Unlock()
		}(i, format)
	}
	wg.Wait()
	return rows, nil
}

// benchOneFormat runs the full create/test/extract cycle for one format and
// returns a single row. Keeping this per-format lets runBenchmark fan out
// without any shared mutable state between workers.
func benchOneFormat(benchDir string, sources []string, original int64, digests map[string][sha256.Size]byte, format string) benchRow {
	row := benchRow{Format: format, Original: original}
	target := filepath.Join(benchDir, fmt.Sprintf("bench.%s", strings.ReplaceAll(format, ".", "-")))
	start := time.Now()
	result, err := archive.Create(sources, target, format, "5", "")
	row.CreateMS = elapsedMS(start)
	fail := func(why string) benchRow {
		row.Error = why
		return row
	}
	if err != nil {
		return fail(err.Error())
	}
	if !result.OK {
		return fail(strings.TrimSpace(result.Stderr))
	}
	if info, statErr := os.Stat(target); statErr == nil {
		row.Archive = info.Size()
		if original > 0 {
			row.Ratio = float64(row.Archive) / float64(original)
		}
	}
	row.CreateSpeed = mbps(original, row.CreateMS)

	start = time.Now()
	testResult, testErr := archive.Test(target, "")
	row.TestMS = elapsedMS(start)
	if testErr != nil {
		return fail(testErr.Error())
	}
	if !testResult.OK {
		return fail(strings.TrimSpace(testResult.Stderr))
	}
	row.TestSpeed = mbps(original, row.TestMS)

	output := filepath.Join(benchDir, "out-"+strings.ReplaceAll(row.Format, ".", "-"))
	if err := os.MkdirAll(output, 0o755); err != nil {
		return fail(err.Error())
	}
	start = time.Now()
	result, err = archive.Extract(target, output, "")
	verifyErr := verifyExtracted(sources, digests, output)
	row.ExtractMS = elapsedMS(start)
	switch {
	case err != nil:
		return fail(err.Error())
	case !result.OK:
		return fail(strings.TrimSpace(result.Stderr))
	case verifyErr != nil:
		return fail(verifyErr.Error())
	default:
		row.OK = true
		row.ExtractSpeed = mbps(original, row.ExtractMS)
	}
	return row
}

// writeBenchSamples builds three deterministic but qualitatively different
// inputs: incompressible binary, natural-language text and structured logs.
// A single highly repetitive sample made every format's ratio meaningless.
func writeBenchSamples(dir string) ([]string, int64, error) {
	specs := []struct {
		name string
		size int
		kind string
	}{
		{"random.bin", 4 << 20, "random"},
		{"article.txt", 4 << 20, "text"},
		{"server.log", 4 << 20, "log"},
	}
	sources := make([]string, 0, len(specs))
	var total int64
	for _, spec := range specs {
		path := filepath.Join(dir, spec.name)
		file, err := os.Create(path)
		if err != nil {
			return nil, 0, fmt.Errorf("创建基准样本失败：%w", err)
		}
		buf := make([]byte, 256<<10)
		rngState := uint64(0x9e3779b97f4a7c15) ^ uint64(len(spec.name))*0x2545f4914f6cdd1d
		written := 0
		for written < spec.size {
			n := len(buf)
			if written+n > spec.size {
				n = spec.size - written
			}
			// Advance the generator across chunks: repeating one random block
			// would let a large-window codec such as LZMA2 deduplicate it.
			fillBenchBlock(buf[:n], spec.kind, spec.name, &rngState)
			if _, err := file.Write(buf[:n]); err != nil {
				file.Close()
				return nil, 0, fmt.Errorf("写入基准样本失败：%w", err)
			}
			written += n
		}
		if err := file.Close(); err != nil {
			return nil, 0, fmt.Errorf("关闭基准样本失败：%w", err)
		}
		sources = append(sources, path)
		total += int64(spec.size)
	}
	return sources, total, nil
}

// fillBenchBlock fills buf with a repeating pattern representative of kind.
// For random data the generator state advances across calls so the whole file
// is incompressible, not a repeated high-entropy block.
func fillBenchBlock(buf []byte, kind, name string, state *uint64) {
	switch kind {
	case "random":
		for i := range buf {
			*state ^= *state << 13
			*state ^= *state >> 7
			*state ^= *state << 17
			buf[i] = byte(*state >> 29)
		}
		return
	}
	var pattern []byte
	switch kind {
	case "text":
		words := []string{
			"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog",
			"archive", "compression", "performance", "benchmark", "entropy",
			"dictionary", "window", "checksum", "integrity", "payload", "stream",
			"buffer", "parallel", "solid", "header", "metadata", "entry", "format",
		}
		var b strings.Builder
		for i := 0; i < 320; i++ {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(words[(i*7+len(name))%len(words)])
		}
		b.WriteByte('\n')
		pattern = []byte(b.String())
	case "log":
		var b strings.Builder
		for i := 0; i < 12; i++ {
			fmt.Fprintf(&b,
				"2026-11-20T12:%02d:%02d.123Z INFO worker=%d request=%d path=/api/v1/items status=200 latency=%dms message=\"processed batch %d\"\n",
				i%60, i, i%8, i*977, 12+i*3, i*31,
			)
		}
		pattern = []byte(b.String())
	}
	for off := 0; off < len(buf); off += len(pattern) {
		copy(buf[off:], pattern)
	}
}

func hashSources(sources []string) (map[string][sha256.Size]byte, error) {
	out := make(map[string][sha256.Size]byte, len(sources))
	for _, p := range sources {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		var sum [sha256.Size]byte
		copy(sum[:], h.Sum(nil))
		out[filepath.Base(p)] = sum
	}
	return out, nil
}

func verifyExtracted(sources []string, digests map[string][sha256.Size]byte, output string) error {
	for _, p := range sources {
		name := filepath.Base(p)
		want, ok := digests[name]
		if !ok {
			continue
		}
		f, err := os.Open(filepath.Join(output, name))
		if err != nil {
			return fmt.Errorf("缺少解压文件 %s：%w", name, err)
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return fmt.Errorf("读取解压文件 %s 失败：%w", name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("关闭解压文件 %s 失败：%w", name, closeErr)
		}
		var got [sha256.Size]byte
		copy(got[:], h.Sum(nil))
		if got != want {
			return fmt.Errorf("解压文件内容不匹配：%s", name)
		}
	}
	return nil
}

func elapsedMS(start time.Time) float64 {
	return math.Round(time.Since(start).Seconds()*100000) / 100
}

func formatBenchRows(rows []benchRow) []string {
	lines := []string{fmt.Sprintf("%-10s %12s %12s %8s %11s %11s %11s", "格式", "原始", "归档", "比率", "创建", "校验", "解压")}
	for _, row := range rows {
		status := ""
		if !row.OK {
			status = "  ! " + row.Error
		}
		lines = append(lines, fmt.Sprintf("%-10s %12s %12s %7.1f%% %11s %11s %11s%s",
			row.Format, humanBytes(row.Original), humanBytes(row.Archive), row.Ratio*100,
			humanSpeed(row.CreateSpeed), humanSpeed(row.TestSpeed), humanSpeed(row.ExtractSpeed), status))
	}
	return lines
}

func mbps(bytes int64, ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	return float64(bytes) / (ms / 1000) / (1024 * 1024)
}

func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB"}
	v := float64(n) / 1024
	for _, unit := range units {
		if v < 1024 || unit == "GB" {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
		v /= 1024
	}
	return fmt.Sprintf("%.1f GB", v)
}

func humanSpeed(v float64) string {
	if v >= 1024 {
		return fmt.Sprintf("%.2f GB/s", v/1024)
	}
	return fmt.Sprintf("%.1f MB/s", v)
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

type splitManifest struct {
	Source   string `json:"source"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Parts    int    `json:"parts"`
	Volume   int64  `json:"volume_size"`
	Created  string `json:"created_at"`
	PartName string `json:"part_prefix"`
}

func splitCmd() *cobra.Command {
	var volumeSize string
	cmd := &cobra.Command{
		Use:   "split <file>",
		Short: "把文件或归档切成多个分卷",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			size, err := parseSize(volumeSize)
			if err != nil {
				return err
			}
			input := args[0]
			if jsonOutput {
				result, err := splitFile(input, size)
				if err != nil {
					return err
				}
				return json.NewEncoder(os.Stdout).Encode(result)
			}
			if useTUI() {
				return runTUI(tuiJob{
					command: "split",
					target:  input,
					work: func() tuiOutcome {
						result, err := splitFile(input, size)
						if err != nil {
							return tuiOutcome{lines: []string{err.Error()}, failed: true, err: err}
						}
						return tuiOutcome{lines: []string{
							tuiKV("分卷数", strconv.Itoa(result.Parts)),
							tuiKV("分卷大小", humanBytes(result.Volume)),
							tuiKV("清单", result.Manifest),
						}}
					},
				})
			}
			result, err := splitFile(input, size)
			if err != nil {
				return err
			}
			fmt.Printf("已生成 %d 个分卷，每个 %s。\n", result.Parts, humanBytes(result.Volume))
			fmt.Println("清单：", result.Manifest)
			return nil
		},
	}
	cmd.Flags().StringVar(&volumeSize, "size", "50M", "分卷大小，如 10K / 5M / 1G")
	return cmd
}

type splitResult struct {
	Parts    int    `json:"parts"`
	Volume   int64  `json:"volume_size"`
	Manifest string `json:"manifest"`
}

func splitFile(input string, volumeSize int64) (splitResult, error) {
	if volumeSize <= 0 {
		return splitResult{}, fmt.Errorf("分卷大小必须大于 0")
	}
	file, err := os.Open(input)
	if err != nil {
		return splitResult{}, fmt.Errorf("无法读取分卷源文件：%w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return splitResult{}, fmt.Errorf("无法读取源文件信息：%w", err)
	}
	if info.IsDir() {
		return splitResult{}, fmt.Errorf("分卷源文件不能是目录")
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return splitResult{}, fmt.Errorf("计算源文件摘要失败：%w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return splitResult{}, fmt.Errorf("无法重置源文件：%w", err)
	}

	prefix := filepath.Clean(input) + ".part"
	parts := 0
	buf := make([]byte, 256<<10)
	for {
		partPath := fmt.Sprintf("%s-%03d", prefix, parts+1)
		out, err := os.OpenFile(partPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return splitResult{}, fmt.Errorf("创建分卷失败：%w", err)
		}
		remaining := volumeSize
		for remaining > 0 {
			limit := int64(len(buf))
			if remaining < limit {
				limit = remaining
			}
			n, readErr := file.Read(buf[:limit])
			if n > 0 {
				if _, writeErr := out.Write(buf[:n]); writeErr != nil {
					out.Close()
					return splitResult{}, fmt.Errorf("写入分卷失败：%w", writeErr)
				}
				remaining -= int64(n)
			}
			if readErr == io.EOF {
				remaining = 0
				break
			}
			if readErr != nil {
				out.Close()
				return splitResult{}, fmt.Errorf("读取源文件失败：%w", readErr)
			}
		}
		if err := out.Close(); err != nil {
			return splitResult{}, fmt.Errorf("关闭分卷失败：%w", err)
		}
		parts++
		if info.Size() == 0 {
			break
		}
		offset := int64(parts) * volumeSize
		if offset >= info.Size() {
			break
		}
	}

	manifest := splitManifest{
		Source:   filepath.Base(input),
		Size:     info.Size(),
		SHA256:   hex.EncodeToString(hasher.Sum(nil)),
		Parts:    parts,
		Volume:   volumeSize,
		Created:  time.Now().UTC().Format(time.RFC3339),
		PartName: prefix + "-%03d",
	}
	manifestPath := filepath.Clean(input) + ".zip233-split.json"
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return splitResult{}, err
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return splitResult{}, fmt.Errorf("写入分卷清单失败：%w", err)
	}
	return splitResult{Parts: parts, Volume: volumeSize, Manifest: manifestPath}, nil
}

func joinCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "join <part-001> [part-002 ...]",
		Short: "合并 zip233 分卷并校验摘要",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput {
				result, err := joinFiles(args, output)
				if err != nil {
					return err
				}
				return json.NewEncoder(os.Stdout).Encode(result)
			}
			if useTUI() {
				return runTUI(tuiJob{
					command: "join",
					target:  strings.Join(args, " · "),
					work: func() tuiOutcome {
						result, err := joinFiles(args, output)
						if err != nil {
							return tuiOutcome{lines: []string{err.Error()}, failed: true, err: err}
						}
						return tuiOutcome{lines: []string{
							tuiKV("输出", result.Output),
							tuiKV("大小", humanBytes(result.Size)),
							tuiKV("校验", "SHA-256 通过"),
						}}
					},
				})
			}
			result, err := joinFiles(args, output)
			if err != nil {
				return err
			}
			fmt.Printf("已合并到 %s（%s），SHA-256 校验通过。\n", result.Output, humanBytes(result.Size))
			return nil
		},
	}
	cmd.Flags().StringVar(&output, "output", "", "输出文件（默认使用清单中的原始文件名）")
	return cmd
}

type joinResult struct {
	Output string `json:"output"`
	Size   int64  `json:"size"`
}

func joinFiles(parts []string, output string) (joinResult, error) {
	first := filepath.Clean(parts[0])
	manifestPath := strings.SplitN(first, ".part-", 2)[0] + ".zip233-split.json"
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return joinResult{}, fmt.Errorf("找不到分卷清单 %s：%w", manifestPath, err)
	}
	var manifest splitManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return joinResult{}, fmt.Errorf("分卷清单无效：%w", err)
	}

	files := parts
	if len(parts) == 1 {
		files = make([]string, 0, manifest.Parts)
		for i := 1; i <= manifest.Parts; i++ {
			files = append(files, fmt.Sprintf(manifest.PartName, i))
		}
	}
	if len(files) != manifest.Parts {
		return joinResult{}, fmt.Errorf("分卷数量不匹配：清单要求 %d 个，实际 %d 个", manifest.Parts, len(files))
	}

	destination := output
	if destination == "" {
		destination = manifest.Source
		if filepath.Dir(first) != "." {
			destination = filepath.Join(filepath.Dir(first), destination)
		}
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return joinResult{}, fmt.Errorf("创建合并输出失败：%w", err)
	}
	hasher := sha256.New()
	multi := io.MultiWriter(out, hasher)
	var total int64
	for _, part := range files {
		file, err := os.Open(part)
		if err != nil {
			out.Close()
			return joinResult{}, fmt.Errorf("打开分卷失败：%w", err)
		}
		n, err := io.Copy(multi, file)
		file.Close()
		total += n
		if err != nil {
			out.Close()
			return joinResult{}, fmt.Errorf("合并分卷失败：%w", err)
		}
	}
	if err := out.Close(); err != nil {
		return joinResult{}, fmt.Errorf("关闭合并输出失败：%w", err)
	}
	if total != manifest.Size {
		os.Remove(destination)
		return joinResult{}, fmt.Errorf("合并后大小不匹配：期望 %d，实际 %d", manifest.Size, total)
	}
	if hex.EncodeToString(hasher.Sum(nil)) != manifest.SHA256 {
		os.Remove(destination)
		return joinResult{}, fmt.Errorf("合并后 SHA-256 与清单不匹配")
	}
	return joinResult{Output: destination, Size: total}, nil
}

func parseSize(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	if value == "" {
		return 0, fmt.Errorf("分卷大小不能为空")
	}
	units := map[string]int64{"K": 1 << 10, "KB": 1 << 10, "M": 1 << 20, "MB": 1 << 20, "G": 1 << 30, "GB": 1 << 30}
	factor := int64(1)
	for suffix, unit := range units {
		if strings.HasSuffix(value, suffix) {
			factor = unit
			value = strings.TrimSpace(strings.TrimSuffix(value, suffix))
			break
		}
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("无法识别分卷大小 %q", value)
	}
	return n * factor, nil
}
