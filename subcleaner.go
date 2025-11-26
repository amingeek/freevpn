// connectivity_tester.go - برنامه تست اتصال جامع با منو
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Modes
const (
	QUICK_TEST  = 1
	FULL_TEST   = 2
	BENCH_MODE  = 3
	INTERACTIVE = 4
)

type Config struct {
	mode        int
	inFile      string
	outFile     string
	concurrency int
	timeout     time.Duration
	verbose     bool
	showDetails bool
}

type TestResult struct {
	link    string
	host    string
	port    string
	isOk    bool
	latency time.Duration
	error   string
}

type TestStats struct {
	total      int64
	success    int64
	failed     int64
	startTime  time.Time
	endTime    time.Time
	minLatency time.Duration
	maxLatency time.Duration
	totalTime  time.Duration
}

var stats TestStats
var statsMutex sync.Mutex

func main() {
	cfg := Config{
		inFile:      "subs.txt",
		outFile:     "good.txt",
		concurrency: 20,
		timeout:     5 * time.Second,
		verbose:     false,
		showDetails: true,
	}

	// نمایش منوی اصلی
	cfg.mode = showMainMenu()

	switch cfg.mode {
	case QUICK_TEST:
		runQuickTest(&cfg)
	case FULL_TEST:
		runFullTest(&cfg)
	case BENCH_MODE:
		runBenchmarkMode(&cfg)
	case INTERACTIVE:
		runInteractiveMode(&cfg)
	default:
		fmt.Println("❌ حالت نامشخص!")
		os.Exit(1)
	}
}

func showMainMenu() int {
	fmt.Clear()
	fmt.Println("╔═══════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    🔍 برنامه تست اتصال سرورها 🔍                             ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════════════════════╝")
	fmt.Println()
	fmt.Println("  📋 انتخاب حالت تست:")
	fmt.Println()
	fmt.Println("    1️⃣  Quick Test      - تست سریع با هاست‌های نمونه")
	fmt.Println("    2️⃣  Full Test       - تست کامل فایل‌های subscription")
	fmt.Println("    3️⃣  Benchmark Mode  - تست عملکرد و latency دقیق")
	fmt.Println("    4️⃣  Interactive     - حالت تعاملی (تنظیم دستی پارامتر‌ها)")
	fmt.Println()
	fmt.Print("  ➜ انتخاب خود را وارد کنید (1-4): ")

	var choice int
	fmt.Scanln(&choice)

	if choice < 1 || choice > 4 {
		fmt.Println("\n❌ انتخاب نامعتبر! استفاده از Quick Test...")
		time.Sleep(1 * time.Second)
		return QUICK_TEST
	}

	return choice
}

// ═════════════════════════════════════════════════════════════════════════════
// QUICK TEST - تست سریع
// ═════════════════════════════════════════════════════════════════════════════

func runQuickTest(cfg *Config) {
	fmt.Clear()
	fmt.Println("╔═══════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                         ⚡ QUICK TEST MODE ⚡                                  ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════════════════════╝\n")

	testCases := []struct {
		name string
		host string
		port string
	}{
		{"Google DNS", "8.8.8.8", "53"},
		{"Cloudflare DNS", "1.1.1.1", "53"},
		{"GitHub HTTPS", "github.com", "443"},
		{"AWS", "aws.amazon.com", "443"},
		{"Localhost HTTP", "127.0.0.1", "80"},
		{"Localhost HTTPS", "127.0.0.1", "443"},
	}

	fmt.Printf("تعداد تست‌ها: %d\n", len(testCases))
	fmt.Printf("تعداد Worker: %d\n", cfg.concurrency)
	fmt.Printf("Timeout: %v\n\n", cfg.timeout)

	runTests(cfg, testCases)
}

// ═════════════════════════════════════════════════════════════════════════════
// FULL TEST - تست کامل
// ═════════════════════════════════════════════════════════════════════════════

func runFullTest(cfg *Config) {
	fmt.Clear()
	fmt.Println("╔═══════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                        📦 FULL TEST MODE 📦                                   ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════════════════════╝\n")

	lines, err := readLines(cfg.inFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ خطا در خواندن فایل: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ %d subscription URL لود شد\n", len(lines))
	fmt.Println("⏳ در حال دریافت و استخراج لینک‌ها...\n")

	allLinks := fetchAndExtractLinks(lines, cfg)

	fmt.Printf("\n✓ %d لینک منحصر به‌فرد پیدا شد\n", len(allLinks))
	fmt.Printf("🔍 در حال تست اتصال‌ها...\n\n")

	testCases := make([]struct {
		name string
		host string
		port string
	}, len(allLinks))

	for i, link := range allLinks {
		h, p, err := parseHostPortFromLink(link)
		if err == nil {
			testCases[i].name = fmt.Sprintf("Link-%d", i+1)
			testCases[i].host = h
			testCases[i].port = p
		}
	}

	runTests(cfg, testCases)
	saveResults(cfg.outFile, allLinks)
}

// ═════════════════════════════════════════════════════════════════════════════
// BENCHMARK MODE - تست عملکرد
// ═════════════════════════════════════════════════════════════════════════════

func runBenchmarkMode(cfg *Config) {
	fmt.Clear()
	fmt.Println("╔═══════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                      ⚙️  BENCHMARK MODE ⚙️                                    ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════════════════════╝\n")

	fmt.Println("تست سرعت مختلف Concurrency Levels:\n")

	concurrencyLevels := []int{1, 5, 10, 20, 50}
	benchCases := []struct {
		name string
		host string
		port string
	}{
		{"Test-1", "8.8.8.8", "53"},
		{"Test-2", "1.1.1.1", "53"},
		{"Test-3", "github.com", "443"},
		{"Test-4", "aws.amazon.com", "443"},
		{"Test-5", "google.com", "443"},
	}

	results := make([]map[string]interface{}, 0)

	for _, concLevel := range concurrencyLevels {
		cfg.concurrency = concLevel
		fmt.Printf("\n🔄 Concurrency Level: %d\n", concLevel)
		fmt.Println(strings.Repeat("─", 80))

		start := time.Now()
		stats = TestStats{
			startTime: start,
		}

		runTests(cfg, benchCases)

		duration := time.Since(start)
		fmt.Printf("\n⏱️  زمان کل: %v\n", duration)
		fmt.Printf("📊 تعداد تست: %d\n", len(benchCases))
		fmt.Printf("⚡ تست/ثانیه: %.2f\n\n", float64(len(benchCases))/duration.Seconds())

		results = append(results, map[string]interface{}{
			"concurrency": concLevel,
			"duration":    duration.String(),
			"testsPerSec": float64(len(benchCases)) / duration.Seconds(),
		})
	}

	fmt.Println("\n" + strings.Repeat("═", 80))
	fmt.Println("📈 خلاصه Benchmark:")
	fmt.Println(strings.Repeat("═", 80))
	fmt.Printf("%-15s | %-20s | %-20s\n", "Concurrency", "Duration", "Tests/Sec")
	fmt.Println(strings.Repeat("─", 60))
	for _, r := range results {
		fmt.Printf("%-15d | %-20s | %-20.2f\n",
			r["concurrency"].(int),
			r["duration"].(string),
			r["testsPerSec"].(float64))
	}
}

// ═════════════════════════════════════════════════════════════════════════════
// INTERACTIVE MODE - حالت تعاملی
// ═════════════════════════════════════════════════════════════════════════════

func runInteractiveMode(cfg *Config) {
	fmt.Clear()
	fmt.Println("╔═══════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                     🎮 INTERACTIVE MODE 🎮                                    ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════════════════════╝\n")

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("📁 فایل ورودی (default: subs.txt): ")
	input, _ := reader.ReadString('\n')
	if strings.TrimSpace(input) != "" {
		cfg.inFile = strings.TrimSpace(input)
	}

	fmt.Print("💾 فایل خروجی (default: good.txt): ")
	input, _ = reader.ReadString('\n')
	if strings.TrimSpace(input) != "" {
		cfg.outFile = strings.TrimSpace(input)
	}

	fmt.Print("⚙️  تعداد Worker (default: 20): ")
	input, _ = reader.ReadString('\n')
	if strings.TrimSpace(input) != "" {
		fmt.Sscanf(strings.TrimSpace(input), "%d", &cfg.concurrency)
	}

	fmt.Print("⏱️  Timeout بر حسب ثانیه (default: 5): ")
	input, _ = reader.ReadString('\n')
	if strings.TrimSpace(input) != "" {
		var timeoutSec int
		fmt.Sscanf(strings.TrimSpace(input), "%d", &timeoutSec)
		cfg.timeout = time.Duration(timeoutSec) * time.Second
	}

	fmt.Print("🔍 Verbose mode? (y/n, default: n): ")
	input, _ = reader.ReadString('\n')
	cfg.verbose = strings.TrimSpace(input) == "y"

	fmt.Println("\n✓ تنظیمات:")
	fmt.Printf("  • فایل ورودی: %s\n", cfg.inFile)
	fmt.Printf("  • فایل خروجی: %s\n", cfg.outFile)
	fmt.Printf("  • Concurrency: %d\n", cfg.concurrency)
	fmt.Printf("  • Timeout: %v\n", cfg.timeout)
	fmt.Printf("  • Verbose: %v\n\n", cfg.verbose)

	// اجرای Full Test با تنظیمات
	lines, err := readLines(cfg.inFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ خطا: %v\n", err)
		return
	}

	allLinks := fetchAndExtractLinks(lines, cfg)

	testCases := make([]struct {
		name string
		host string
		port string
	}, len(allLinks))

	for i, link := range allLinks {
		h, p, err := parseHostPortFromLink(link)
		if err == nil {
			testCases[i].name = fmt.Sprintf("Link-%d", i+1)
			testCases[i].host = h
			testCases[i].port = p
		}
	}

	runTests(cfg, testCases)
	saveResults(cfg.outFile, allLinks)
}

// ═════════════════════════════════════════════════════════════════════════════
// توابع کمکی
// ═════════════════════════════════════════════════════════════════════════════

func runTests(cfg *Config, testCases []struct {
	name string
	host string
	port string
}) {
	stats = TestStats{
		startTime: time.Now(),
		total:     int64(len(testCases)),
	}

	jobs := make(chan struct {
		name string
		host string
		port string
	}, len(testCases))
	results := make(chan TestResult, len(testCases))

	var wg sync.WaitGroup

	// شروع Workers
	for w := 0; w < cfg.concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				result := testConnection(job.host, job.port, cfg.timeout)
				results <- result
			}
		}()
	}

	// ارسال کارها
	go func() {
		for _, tc := range testCases {
			jobs <- tc
		}
		close(jobs)
	}()

	// جمع‌آوری نتایج
	successCount := int64(0)
	go func() {
		wg.Wait()
		close(results)
	}()

	fmt.Println(strings.Repeat("─", 80))
	for result := range results {
		if result.isOk {
			fmt.Printf("✓ %-20s %-25s:%-10s [%8v]\n",
				"OK", result.host, result.port, result.latency)
			atomic.AddInt64(&stats.success, 1)
			successCount++

			if stats.minLatency == 0 || result.latency < stats.minLatency {
				statsMutex.Lock()
				stats.minLatency = result.latency
				statsMutex.Unlock()
			}
			if result.latency > stats.maxLatency {
				statsMutex.Lock()
				stats.maxLatency = result.latency
				statsMutex.Unlock()
			}
		} else {
			fmt.Printf("❌ %-20s %-25s:%-10s [%s]\n",
				"FAIL", result.host, result.port, result.error)
			atomic.AddInt64(&stats.failed, 1)
		}
	}

	stats.endTime = time.Now()
	stats.totalTime = stats.endTime.Sub(stats.startTime)

	// نمایش خلاصه
	fmt.Println(strings.Repeat("═", 80))
	fmt.Println("📊 نتایج:")
	fmt.Printf("  کل تست‌ها:        %d\n", stats.total)
	fmt.Printf("  موفق:             %d ✓\n", atomic.LoadInt64(&stats.success))
	fmt.Printf("  ناموفق:           %d ❌\n", atomic.LoadInt64(&stats.failed))
	fmt.Printf("  درصد موفقیت:      %.2f%%\n", float64(atomic.LoadInt64(&stats.success))*100/float64(stats.total))
	fmt.Printf("  زمان کل:          %v\n", stats.totalTime)
	fmt.Printf("  Min Latency:      %v\n", stats.minLatency)
	fmt.Printf("  Max Latency:      %v\n", stats.maxLatency)
	if stats.success > 0 {
		fmt.Printf("  Avg Latency:      %v\n", stats.totalTime/time.Duration(stats.success))
	}
	fmt.Println(strings.Repeat("═", 80))
}

func testConnection(host, port string, timeout time.Duration) TestResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	latency := time.Since(start)

	if err != nil {
		return TestResult{
			host:    host,
			port:    port,
			isOk:    false,
			latency: latency,
			error:   fmt.Sprintf("%v", err),
		}
	}
	defer conn.Close()

	return TestResult{
		host:    host,
		port:    port,
		isOk:    true,
		latency: latency,
		error:   "",
	}
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		s := strings.TrimSpace(scanner.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		lines = append(lines, s)
	}
	return lines, scanner.Err()
}

func fetchAndExtractLinks(urls []string, cfg *Config) []string {
	var allLinks []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for _, u := range urls {
		wg.Add(1)
		sem <- struct{}{}
		go func(urlstr string) {
			defer wg.Done()
			defer func() { <-sem }()
			body, err := fetchURL(urlstr)
			if err == nil {
				links := extractLinks(body)
				mu.Lock()
				allLinks = append(allLinks, links...)
				mu.Unlock()
			}
		}(u)
	}
	wg.Wait()

	// Deduplicate
	uniqMap := make(map[string]struct{})
	unique := []string{}
	for _, l := range allLinks {
		if _, ok := uniqMap[l]; !ok {
			uniqMap[l] = struct{}{}
			unique = append(unique, l)
		}
	}

	return unique
}

func fetchURL(u string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func extractLinks(raw string) []string {
	links := []string{}
	re := regexp.MustCompile(`(?i)(vmess://|vless://|trojan://|ss://)[^\s'"]+`)
	matches := re.FindAllString(raw, -1)
	for _, m := range matches {
		links = append(links, strings.TrimSpace(m))
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if looksBase64(line) {
			links = append(links, "vmess://"+line)
		}
	}
	uniq := make(map[string]struct{})
	out := []string{}
	for _, l := range links {
		if _, ok := uniq[l]; !ok {
			uniq[l] = struct{}{}
			out = append(out, l)
		}
	}
	return out
}

func looksBase64(s string) bool {
	s = strings.TrimPrefix(s, "vmess://")
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	return true
}

func parseHostPortFromLink(link string) (host, port string, err error) {
	u := strings.TrimSpace(link)
	if strings.HasPrefix(u, "vmess://") {
		s := strings.TrimPrefix(u, "vmess://")
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			b, err = base64.RawStdEncoding.DecodeString(s)
			if err != nil {
				return "", "", err
			}
		}
		var j map[string]interface{}
		if err := json.Unmarshal(b, &j); err != nil {
			return "", "", err
		}
		h, ok := j["add"].(string)
		if !ok || h == "" {
			h, ok = j["server"].(string)
			if !ok || h == "" {
				return "", "", fmt.Errorf("no host")
			}
		}
		host = h
		switch p := j["port"].(type) {
		case string:
			port = p
		case float64:
			port = fmt.Sprintf("%.0f", p)
		default:
			port = "443"
		}
		return host, port, nil
	}
	if !strings.Contains(u, "://") {
		return "", "", fmt.Errorf("unknown scheme")
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return "", "", err
	}
	host = parsed.Hostname()
	port = parsed.Port()
	if host != "" && port != "" {
		return host, port, nil
	}
	return "", "", fmt.Errorf("cannot extract host/port")
}

func saveResults(filename string, links []string) {
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("❌ خطا در ذخیره: %v\n", err)
		return
	}
	defer f.Close()
	for _, link := range links {
		fmt.Fprintf(f, "%s\n", link)
	}
	fmt.Printf("\n✓ نتایج در %s ذخیره شدند\n", filename)
}
