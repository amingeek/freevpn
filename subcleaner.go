// connectivity_ultimate.go - نسخه نهایی با Arrow Keys و بیشتر Concurrency
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"golang.org/x/term"
	"syscall"
)

const (
	QUICK_TEST  = 0
	FULL_TEST   = 1
	BENCH_MODE  = 2
	INTERACTIVE = 3
	EXIT        = 4
)

type Config struct {
	mode        int
	inFile      string
	outFile     string
	concurrency int
	timeout     time.Duration
	verbose     bool
}

type TestResult struct {
	link     string
	host     string
	port     string
	isOk     bool
	latency  time.Duration
	error    string
	linkType string
}

type TestStats struct {
	total      int64
	success    int64
	failed     int64
	startTime  time.Time
	minLatency time.Duration
	maxLatency time.Duration
}

var stats TestStats
var statsMutex sync.Mutex

type ConfigByType struct {
	vless  []string
	vmess  []string
	ss     []string
	trojan []string
	other  []string
}

func main() {
	for {
		choice := showArrowMenu()
		if choice == EXIT {
			clearScreen()
			fmt.Println("\n👋 خدا حافظ!\n")
			break
		}

		cfg := Config{
			inFile:      "subs.txt",
			outFile:     "good.txt",
			concurrency: 100, // بیشتر برای سرعت بیشتر
			timeout:     5 * time.Second,
			verbose:     false,
		}

		switch choice {
		case QUICK_TEST:
			runQuickTest(&cfg)
		case FULL_TEST:
			runFullTest(&cfg)
		case BENCH_MODE:
			runBenchmarkMode(&cfg)
		case INTERACTIVE:
			runInteractiveMode(&cfg)
		}

		fmt.Print("\n\nبرای ادامه Enter را فشار دهید...")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                    ARROW KEY MENU                              ║
// ╚════════════════════════════════════════════════════════════════╝

func showArrowMenu() int {
	clearScreen()

	menuItems := []string{
		"⚡ Quick Test      - تست سریع (6 هاست نمونه)",
		"📦 Full Test       - تست کامل (فایل subscription)",
		"⚙️  Benchmark Mode  - مقایسه عملکرد",
		"🎮 Interactive     - حالت تعاملی کامل",
		"❌ Exit            - خروج",
	}

	selectedIndex := 0

	for {
		clearScreen()
		fmt.Println("╔════════════════════════════════════════════════════════════════╗")
		fmt.Println("║            🔍 برنامه تست اتصال سرورها (ULTIMATE) 🔍          ║")
		fmt.Println("╚════════════════════════════════════════════════════════════════╝\n")

		fmt.Println("📋 انتخاب حالت تست (↑ ↓ + Enter):\n")

		for i, item := range menuItems {
			if i == selectedIndex {
				fmt.Printf("  ➜ %s  ◄──\n", item)
			} else {
				fmt.Printf("    %s\n", item)
			}
		}

		fmt.Println("\n  ⌨️  فلش‌های بالا/پایین برای انتخاب")
		fmt.Println("  ⌨️  Enter برای تأیید")

		// خواندن input بدون نیاز Enter فوری
		key := readArrowKey()

		if key == "up" {
			selectedIndex = (selectedIndex - 1 + len(menuItems)) % len(menuItems)
		} else if key == "down" {
			selectedIndex = (selectedIndex + 1) % len(menuItems)
		} else if key == "enter" {
			return selectedIndex
		} else if key == "q" {
			return EXIT
		}
	}
}

// readArrowKey - خواندن کلیدهای جهت‌نما
func readArrowKey() string {
	oldState, err := term.MakeRaw(int(syscall.Stdin))
	if err != nil {
		// Fallback برای سیستم‌های بدون support terminal
		return readSimpleInput()
	}
	defer term.Restore(int(syscall.Stdin), oldState)

	b := make([]byte, 3)
	n, _ := os.Stdin.Read(b)

	if n == 1 {
		if b[0] == 13 {
			return "enter" // Enter
		} else if b[0] == 'q' || b[0] == 'Q' {
			return "q" // Quit
		}
	} else if n == 3 {
		// ANSI escape sequence
		if b[0] == 27 && b[1] == 91 {
			if b[2] == 65 {
				return "up" // ↑
			} else if b[2] == 66 {
				return "down" // ↓
			}
		}
	}

	return ""
}

// readSimpleInput - Fallback برای سیستم‌های بدون terminal
func readSimpleInput() string {
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" || input == "enter" {
		return "enter"
	} else if input == "q" || input == "Q" {
		return "q"
	}

	return ""
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                      PROGRESS BAR                              ║
// ╚════════════════════════════════════════════════════════════════╝

func showProgressBar(current, total int64) {
	if total == 0 {
		return
	}

	percent := float64(current) * 100 / float64(total)
	filled := int(percent / 2)
	empty := 50 - filled

	// سرعت‌تر: فقط نمایش یک‌بار
	fmt.Printf("\r[")
	for i := 0; i < filled; i++ {
		fmt.Print("█")
	}
	for i := 0; i < empty; i++ {
		fmt.Print("░")
	}
	fmt.Printf("] %.1f%% (%d/%d)   ", percent, current, total)
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                    QUICK TEST MODE                             ║
// ╚════════════════════════════════════════════════════════════════╝

func runQuickTest(cfg *Config) {
	clearScreen()
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                   ⚡ QUICK TEST MODE ⚡                         ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝\n")

	testCases := []struct {
		name string
		host string
		port string
	}{
		{"Google DNS", "8.8.8.8", "53"},
		{"Cloudflare DNS", "1.1.1.1", "53"},
		{"GitHub", "github.com", "443"},
		{"AWS", "aws.amazon.com", "443"},
		{"Localhost HTTP", "127.0.0.1", "80"},
		{"Localhost HTTPS", "127.0.0.1", "443"},
	}

	fmt.Printf("🔄 تعداد تست‌ها: %d\n", len(testCases))
	fmt.Printf("👷 تعداد Worker: %d (بیشتر برای سرعت)\n", cfg.concurrency)
	fmt.Printf("⏱️  Timeout: %v\n\n", cfg.timeout)

	_ = runTestsWithProgress(cfg, testCases)
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                    FULL TEST MODE                              ║
// ╚════════════════════════════════════════════════════════════════╝

func runFullTest(cfg *Config) {
	clearScreen()
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                   📦 FULL TEST MODE 📦                         ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝\n")

	lines, err := readLines(cfg.inFile)
	if err != nil {
		fmt.Printf("❌ خطا: %v\n", err)
		return
	}

	fmt.Printf("📥 %d URL لود شد\n", len(lines))
	fmt.Println("⏳ در حال دریافت لینک‌ها...\n")

	allLinks := fetchAndExtractLinksConcurrent(lines)

	fmt.Printf("✓ %d لینک منحصر به‌فرد یافت شد\n\n", len(allLinks))
	fmt.Println("🔍 در حال تست اتصال‌ها...\n")

	testCases := make([]struct {
		name string
		host string
		port string
	}, 0)

	for _, link := range allLinks {
		h, p, err := parseHostPortFromLink(link)
		if err == nil {
			testCases = append(testCases, struct {
				name string
				host string
				port string
			}{link, h, p})
		}
	}

	results := runTestsWithProgress(cfg, testCases)
	saveConfigsByType(results)
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                  BENCHMARK MODE                                ║
// ╚════════════════════════════════════════════════════════════════╝

func runBenchmarkMode(cfg *Config) {
	clearScreen()
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                  ⚙️  BENCHMARK MODE ⚙️                          ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝\n")

	concurrencyLevels := []int{10, 25, 50, 100, 200}
	testCases := []struct {
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

	fmt.Println("Concurrency | Duration | Tests/Sec | Success Rate")
	fmt.Println(strings.Repeat("─", 60))

	for _, concLevel := range concurrencyLevels {
		cfg.concurrency = concLevel
		start := time.Now()
		stats = TestStats{startTime: start}

		_ = runTestsWithProgress(cfg, testCases)

		duration := time.Since(start)
		success := atomic.LoadInt64(&stats.success)
		percent := float64(success) * 100 / float64(len(testCases))

		fmt.Printf("%-11d | %8v | %9.2f | %.1f%%\n",
			concLevel,
			duration,
			float64(len(testCases))/duration.Seconds(),
			percent)
	}
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                 INTERACTIVE MODE                               ║
// ╚════════════════════════════════════════════════════════════════╝

func runInteractiveMode(cfg *Config) {
	clearScreen()
	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                  🎮 INTERACTIVE MODE 🎮                        ║")
	fmt.Println("╚════════════════════════════════════════════════════════════════╝\n")

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("📁 فایل ورودی (default: subs.txt): ")
	input, _ := reader.ReadString('\n')
	if strings.TrimSpace(input) != "" {
		cfg.inFile = strings.TrimSpace(input)
	}

	fmt.Print("👷 تعداد Worker (default: 100): ")
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

	fmt.Println("\n✓ تنظیمات:")
	fmt.Printf("  📁 فایل: %s\n", cfg.inFile)
	fmt.Printf("  👷 Workers: %d\n", cfg.concurrency)
	fmt.Printf("  ⏱️  Timeout: %v\n\n", cfg.timeout)

	lines, err := readLines(cfg.inFile)
	if err != nil {
		fmt.Printf("❌ خطا: %v\n", err)
		return
	}

	allLinks := fetchAndExtractLinksConcurrent(lines)

	testCases := make([]struct {
		name string
		host string
		port string
	}, 0)

	for _, link := range allLinks {
		h, p, err := parseHostPortFromLink(link)
		if err == nil {
			testCases = append(testCases, struct {
				name string
				host string
				port string
			}{link, h, p})
		}
	}

	results := runTestsWithProgress(cfg, testCases)
	saveConfigsByType(results)
}

// ╔════════════════════════════════════════════════════════════════╗
// ║            TEST EXECUTION WITH PROGRESS (FASTER)               ║
// ╚════════════════════════════════════════════════════════════════╝

func runTestsWithProgress(cfg *Config, testCases []struct {
	name string
	host string
	port string
}) []TestResult {
	stats = TestStats{
		startTime: time.Now(),
		total:     int64(len(testCases)),
	}

	jobs := make(chan struct {
		name string
		host string
		port string
	}, len(testCases)*2) // Buffer بیشتر

	results := make(chan TestResult, len(testCases)*2)
	var wg sync.WaitGroup

	// شروع بیشتر Workers
	numWorkers := cfg.concurrency
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				result := testConnection(job.host, job.port, cfg.timeout)
				result.link = job.name
				results <- result
			}
		}()
	}

	// ارسال کارها (بدون Goroutine جداگانه برای سرعت)
	for _, tc := range testCases {
		jobs <- tc
	}
	close(jobs)

	// جمع‌آوری نتایج
	var allResults []TestResult
	processedCount := int64(0)
	lastUpdate := int64(0)

	go func() {
		wg.Wait()
		close(results)
	}()

	for result := range results {
		processedCount++

		if result.isOk {
			atomic.AddInt64(&stats.success, 1)
			if stats.minLatency == 0 || result.latency < stats.minLatency {
				stats.minLatency = result.latency
			}
			if result.latency > stats.maxLatency {
				stats.maxLatency = result.latency
			}
		} else {
			atomic.AddInt64(&stats.failed, 1)
		}

		allResults = append(allResults, result)

		// نمایش هر 5 عنصر برای سرعت
		if processedCount-lastUpdate >= 5 || processedCount == int64(len(testCases)) {
			showProgressBar(processedCount, int64(len(testCases)))
			lastUpdate = processedCount
		}
	}

	fmt.Println("\n")
	printSummary()

	return allResults
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
			host:     host,
			port:     port,
			isOk:     false,
			latency:  latency,
			error:    fmt.Sprintf("%v", err),
			linkType: "unknown",
		}
	}
	defer conn.Close()

	return TestResult{
		host:     host,
		port:     port,
		isOk:     true,
		latency:  latency,
		error:    "",
		linkType: "unknown",
	}
}

// ╔════════════════════════════════════════════════════════════════╗
// ║              HELPER FUNCTIONS                                  ║
// ╚════════════════════════════════════════════════════════════════╝

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func printSummary() {
	fmt.Println(strings.Repeat("═", 64))
	fmt.Println("📊 خلاصه نتایج:")
	fmt.Printf("  کل تست‌ها:     %d\n", stats.total)
	fmt.Printf("  موفق:          %d ✓\n", atomic.LoadInt64(&stats.success))
	fmt.Printf("  ناموفق:        %d ❌\n", atomic.LoadInt64(&stats.failed))

	if stats.total > 0 {
		percent := float64(atomic.LoadInt64(&stats.success)) * 100 / float64(stats.total)
		fmt.Printf("  درصد موفقیت:   %.1f%%\n", percent)
	}

	if stats.minLatency > 0 {
		fmt.Printf("  Min Latency:   %v\n", stats.minLatency)
		fmt.Printf("  Max Latency:   %v\n", stats.maxLatency)
	}
	fmt.Println(strings.Repeat("═", 64))
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

// fetchAndExtractLinksConcurrent - دریافت سریع‌تر
func fetchAndExtractLinksConcurrent(urls []string) []string {
	var allLinks []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Worker Pool بیشتر
	sem := make(chan struct{}, 32) // 32 concurrent fetches

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

func detectLinkType(link string) string {
	if strings.HasPrefix(link, "vmess://") {
		return "vmess"
	} else if strings.HasPrefix(link, "vless://") {
		return "vless"
	} else if strings.HasPrefix(link, "ss://") {
		return "ss"
	} else if strings.HasPrefix(link, "trojan://") {
		return "trojan"
	}
	return "unknown"
}

func saveConfigsByType(results []TestResult) {
	configs := ConfigByType{
		vless:  []string{},
		vmess:  []string{},
		ss:     []string{},
		trojan: []string{},
		other:  []string{},
	}

	for _, result := range results {
		if !result.isOk {
			continue
		}

		linkType := detectLinkType(result.link)
		switch linkType {
		case "vless":
			configs.vless = append(configs.vless, result.link)
		case "vmess":
			configs.vmess = append(configs.vmess, result.link)
		case "ss":
			configs.ss = append(configs.ss, result.link)
		case "trojan":
			configs.trojan = append(configs.trojan, result.link)
		default:
			configs.other = append(configs.other, result.link)
		}
	}

	fmt.Println("\n💾 ذخیره فایل‌ها...\n")

	if len(configs.vless) > 0 {
		saveToFile("bisub_vless.txt", configs.vless)
	}
	if len(configs.vmess) > 0 {
		saveToFile("bisub_vmess.txt", configs.vmess)
	}
	if len(configs.ss) > 0 {
		saveToFile("bisub_ss.txt", configs.ss)
	}
	if len(configs.trojan) > 0 {
		saveToFile("bisub_trojan.txt", configs.trojan)
	}
	if len(configs.other) > 0 {
		saveToFile("bisub_other.txt", configs.other)
	}

	allConfigs := append(configs.vless, configs.vmess...)
	allConfigs = append(allConfigs, configs.ss...)
	allConfigs = append(allConfigs, configs.trojan...)
	allConfigs = append(allConfigs, configs.other...)

	saveToFile("bisub.txt", allConfigs)

	fmt.Printf("\n✓ کل: %d کانفیگ\n", len(allConfigs))
	fmt.Printf("  • VLESS: %d\n", len(configs.vless))
	fmt.Printf("  • VMESS: %d\n", len(configs.vmess))
	fmt.Printf("  • SS: %d\n", len(configs.ss))
	fmt.Printf("  • Trojan: %d\n", len(configs.trojan))
	fmt.Printf("  • Other: %d\n\n", len(configs.other))

	fmt.Println("📁 فایل‌های ذخیره شده:")
	fmt.Println("  • bisub.txt (همه کانفیگ‌ها)")
	if len(configs.vless) > 0 {
		fmt.Println("  • bisub_vless.txt")
	}
	if len(configs.vmess) > 0 {
		fmt.Println("  • bisub_vmess.txt")
	}
	if len(configs.ss) > 0 {
		fmt.Println("  • bisub_ss.txt")
	}
	if len(configs.trojan) > 0 {
		fmt.Println("  • bisub_trojan.txt")
	}
	if len(configs.other) > 0 {
		fmt.Println("  • bisub_other.txt")
	}
}

func saveToFile(filename string, configs []string) {
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("❌ خطا در ایجاد %s: %v\n", filename, err)
		return
	}
	defer f.Close()

	for _, config := range configs {
		fmt.Fprintf(f, "%s\n", config)
	}

	fmt.Printf("✓ %s: %d کانفیگ\n", filename, len(configs))
}
