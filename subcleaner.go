// connectivity_final.go - Final version with English CLI and Fixed Progress Bar
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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"
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

func getOptimalConcurrency() int {
	numCPU := runtime.NumCPU()
	return numCPU * 50
}

func getOptimalFetchPool() int {
	numCPU := runtime.NumCPU()
	return numCPU * 8
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	for {
		choice := showMenuBeautiful()
		if choice == EXIT {
			clearScreen()
			fmt.Println()
			printBox("Goodbye! 👋", "center")
			fmt.Println()
			break
		}

		cfg := Config{
			inFile:      "subs.txt",
			outFile:     "good.txt",
			concurrency: getOptimalConcurrency(),
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

		fmt.Print("\n")
		fmt.Println("  Press ENTER to continue...")
		bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                    BEAUTIFUL MENU                              ║
// ╚════════════════════════════════════════════════════════════════╝

func showMenuBeautiful() int {
	clearScreen()

	menuItems := []struct {
		icon  string
		title string
		desc  string
	}{
		{"⚡", "Quick Test", "Fast test (6 sample hosts)"},
		{"📦", "Full Test", "Complete test (subscription file)"},
		{"⚙️ ", "Benchmark", "Performance comparison"},
		{"🎮", "Interactive", "Custom settings mode"},
		{"❌", "Exit", "Exit application"},
	}

	selectedIndex := 0

	for {
		clearScreen()

		// Header
		fmt.Println("  ╔═══════════════════════════════════════════════════════════════╗")
		fmt.Println("  ║                                                               ║")
		fmt.Println("  ║      🔥 CONNECTIVITY TESTER - MAX PERFORMANCE (FINAL) 🔥     ║")
		fmt.Println("  ║                                                               ║")
		fmt.Printf("  ║   CPU Cores: %-2d  │  Workers: %-4d  │  Fetch Pool: %-2d  ║\n",
			runtime.NumCPU(), getOptimalConcurrency(), getOptimalFetchPool())
		fmt.Println("  ║                                                               ║")
		fmt.Println("  ╚═══════════════════════════════════════════════════════════════╝")

		fmt.Println()
		fmt.Println("  SELECT TEST MODE (↑ ↓ + ENTER):")
		fmt.Println()

		for i, item := range menuItems {
			if i == selectedIndex {
				fmt.Printf("  ┏━ %s  %-12s - %s  ◄── SELECTED\n", item.icon, item.title, item.desc)
				fmt.Printf("  ┃\n")
			} else {
				fmt.Printf("  ┃  %s  %-12s - %s\n", item.icon, item.title, item.desc)
			}
		}

		fmt.Println()
		fmt.Println("  ⌨️  KEYBOARD:")
		fmt.Println("     ↑ ↓  : Select    │  ENTER : Confirm    │  Q : Quit")
		fmt.Println()

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

func printBox(text string, align string) {
	width := len(text) + 4
	fmt.Print("  ")
	for i := 0; i < width; i++ {
		fmt.Print("═")
	}
	fmt.Println()
	fmt.Printf("  ║ %s ║\n", text)
	fmt.Print("  ")
	for i := 0; i < width; i++ {
		fmt.Print("═")
	}
	fmt.Println()
}

// readArrowKey - Read arrow keys
func readArrowKey() string {
	oldState, err := term.MakeRaw(int(syscall.Stdin))
	if err != nil {
		return readSimpleInput()
	}
	defer term.Restore(int(syscall.Stdin), oldState)

	b := make([]byte, 3)
	n, _ := os.Stdin.Read(b)

	if n == 1 {
		if b[0] == 13 {
			return "enter"
		} else if b[0] == 'q' || b[0] == 'Q' {
			return "q"
		}
	} else if n == 3 {
		if b[0] == 27 && b[1] == 91 {
			if b[2] == 65 {
				return "up"
			} else if b[2] == 66 {
				return "down"
			}
		}
	}

	return ""
}

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
// ║              FIXED PROGRESS BAR                               ║
// ╚════════════════════════════════════════════════════════════════╝

var lastProgressLength int = 0

func showProgressBar(current, total int64, speed float64) {
	if total == 0 {
		return
	}

	percent := float64(current) * 100 / float64(total)
	filled := int(percent / 2)
	empty := 50 - filled

	// Build progress string
	progressStr := fmt.Sprintf(
		"  [%s%s] %5.1f%% (%d/%d) | %.1f tests/sec",
		strings.Repeat("█", filled),
		strings.Repeat("░", empty),
		percent,
		current,
		total,
		speed)

	// Pad with spaces to clear previous content
	if len(progressStr) < lastProgressLength {
		progressStr += strings.Repeat(" ", lastProgressLength-len(progressStr))
	}
	lastProgressLength = len(progressStr)

	fmt.Printf("\r%s", progressStr)
}

func clearProgressBar() {
	if lastProgressLength > 0 {
		fmt.Printf("\r%s\r", strings.Repeat(" ", lastProgressLength))
		lastProgressLength = 0
	}
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                    QUICK TEST MODE                             ║
// ╚════════════════════════════════════════════════════════════════╝

func runQuickTest(cfg *Config) {
	clearScreen()
	fmt.Println()
	printBox("⚡ QUICK TEST MODE", "center")

	testCases := []struct {
		name string
		host string
		port string
	}{
		{"Google DNS", "8.8.8.8", "53"},
		{"Cloudflare DNS", "1.1.1.1", "53"},
		{"GitHub", "github.com", "443"},
		{"AWS", "aws.amazon.com", "443"},
		{"HTTP Localhost", "127.0.0.1", "80"},
		{"HTTPS Localhost", "127.0.0.1", "443"},
	}

	fmt.Println()
	fmt.Printf("  🔄 Total tests:      %d\n", len(testCases))
	fmt.Printf("  👷 Worker count:     %d (CPU: %d cores)\n", cfg.concurrency, runtime.NumCPU())
	fmt.Printf("  ⏱️  Timeout:          %v\n", cfg.timeout)
	fmt.Println()

	_ = runTestsWithProgress(cfg, testCases)
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                    FULL TEST MODE                              ║
// ╚════════════════════════════════════════════════════════════════╝

func runFullTest(cfg *Config) {
	clearScreen()
	fmt.Println()
	printBox("📦 FULL TEST MODE", "center")

	lines, err := readLines(cfg.inFile)
	if err != nil {
		fmt.Printf("  ❌ Error: %v\n", err)
		return
	}

	fmt.Println()
	fmt.Printf("  📥 URLs loaded:      %d\n", len(lines))
	fmt.Printf("  👷 Worker count:     %d (CPU: %d cores)\n", cfg.concurrency, runtime.NumCPU())
	fmt.Printf("  📡 Fetch pool:       %d\n", getOptimalFetchPool())
	fmt.Println()
	fmt.Println("  ⏳ Fetching links...")

	allLinks := fetchAndExtractLinksConcurrent(lines)

	clearProgressBar()
	fmt.Printf("  ✓ Unique links:     %d\n\n", len(allLinks))
	fmt.Println("  🔍 Testing connections...\n")

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
	fmt.Println()
	printBox("⚙️  BENCHMARK MODE", "center")

	concurrencyLevels := []int{
		runtime.NumCPU() * 10,
		runtime.NumCPU() * 25,
		runtime.NumCPU() * 50,
		runtime.NumCPU() * 75,
		runtime.NumCPU() * 100,
	}

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
		{"Test-6", "cloudflare.com", "443"},
		{"Test-7", "8.8.4.4", "53"},
		{"Test-8", "1.0.0.1", "53"},
	}

	fmt.Println()
	fmt.Println("  Concurrency     Duration     Tests/Sec    Success Rate")
	fmt.Println("  " + strings.Repeat("─", 55))

	for _, concLevel := range concurrencyLevels {
		cfg.concurrency = concLevel
		start := time.Now()
		stats = TestStats{startTime: start}

		_ = runTestsWithProgress(cfg, testCases)

		duration := time.Since(start)
		success := atomic.LoadInt64(&stats.success)
		percent := float64(success) * 100 / float64(len(testCases))
		testsPerSec := float64(len(testCases)) / duration.Seconds()

		fmt.Printf("  %-15d %-12v %-11.2f %.1f%%\n",
			concLevel,
			duration,
			testsPerSec,
			percent)
	}
}

// ╔════════════════════════════════════════════════════════════════╗
// ║                 INTERACTIVE MODE                               ║
// ╚════════════════════════════════════════════════════════════════╝

func runInteractiveMode(cfg *Config) {
	clearScreen()
	fmt.Println()
	printBox("🎮 INTERACTIVE MODE", "center")

	reader := bufio.NewReader(os.Stdin)

	fmt.Println()
	fmt.Print("  📁 Input file (default: subs.txt): ")
	input, _ := reader.ReadString('\n')
	if strings.TrimSpace(input) != "" {
		cfg.inFile = strings.TrimSpace(input)
	}

	fmt.Print("  👷 Worker count (default: optimal): ")
	input, _ = reader.ReadString('\n')
	if strings.TrimSpace(input) != "" {
		fmt.Sscanf(strings.TrimSpace(input), "%d", &cfg.concurrency)
	}

	fmt.Print("  ⏱️  Timeout in seconds (default: 5): ")
	input, _ = reader.ReadString('\n')
	if strings.TrimSpace(input) != "" {
		var timeoutSec int
		fmt.Sscanf(strings.TrimSpace(input), "%d", &timeoutSec)
		cfg.timeout = time.Duration(timeoutSec) * time.Second
	}

	fmt.Println()
	fmt.Println("  ✓ Settings:")
	fmt.Printf("    📁 File: %s\n", cfg.inFile)
	fmt.Printf("    👷 Workers: %d\n", cfg.concurrency)
	fmt.Printf("    ⏱️  Timeout: %v\n\n", cfg.timeout)

	lines, err := readLines(cfg.inFile)
	if err != nil {
		fmt.Printf("  ❌ Error: %v\n", err)
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
// ║         TEST EXECUTION WITH FIXED PROGRESS BAR                 ║
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
	}, len(testCases)*4)

	results := make(chan TestResult, len(testCases)*4)
	var wg sync.WaitGroup

	for w := 0; w < cfg.concurrency; w++ {
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

	for _, tc := range testCases {
		jobs <- tc
	}
	close(jobs)

	var allResults []TestResult
	processedCount := int64(0)
	lastUpdate := int64(0)
	startTime := time.Now()

	go func() {
		wg.Wait()
		close(results)
	}()

	fmt.Println()

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

		if processedCount-lastUpdate >= 1 || processedCount == int64(len(testCases)) {
			elapsed := time.Since(startTime).Seconds()
			speed := float64(processedCount) / elapsed
			showProgressBar(processedCount, int64(len(testCases)), speed)
			lastUpdate = processedCount
		}
	}

	clearProgressBar()
	fmt.Println()
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
	fmt.Println("  " + strings.Repeat("═", 60))
	fmt.Println("  📊 TEST RESULTS SUMMARY:")
	fmt.Printf("    Total Tests:       %d\n", stats.total)
	fmt.Printf("    ✓ Successful:      %d\n", atomic.LoadInt64(&stats.success))
	fmt.Printf("    ❌ Failed:         %d\n", atomic.LoadInt64(&stats.failed))

	if stats.total > 0 {
		percent := float64(atomic.LoadInt64(&stats.success)) * 100 / float64(stats.total)
		fmt.Printf("    Success Rate:      %.1f%%\n", percent)
	}

	if stats.minLatency > 0 {
		fmt.Printf("    🟢 Min Latency:    %v\n", stats.minLatency)
		fmt.Printf("    🔴 Max Latency:    %v\n", stats.maxLatency)
	}
	fmt.Println("  " + strings.Repeat("═", 60))
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

func fetchAndExtractLinksConcurrent(urls []string) []string {
	var allLinks []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	fetchPool := getOptimalFetchPool()
	sem := make(chan struct{}, fetchPool)

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

	fmt.Println()
	fmt.Println("  💾 SAVING FILES...\n")

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

	fmt.Printf("\n  ✓ TOTAL: %d configs\n", len(allConfigs))
	fmt.Printf("    • VLESS:  %d\n", len(configs.vless))
	fmt.Printf("    • VMESS:  %d\n", len(configs.vmess))
	fmt.Printf("    • SS:     %d\n", len(configs.ss))
	fmt.Printf("    • Trojan: %d\n", len(configs.trojan))
	fmt.Printf("    • Other:  %d\n\n", len(configs.other))

	fmt.Println("  📁 FILES SAVED:")
	fmt.Println("    • bisub.txt")
	if len(configs.vless) > 0 {
		fmt.Println("    • bisub_vless.txt")
	}
	if len(configs.vmess) > 0 {
		fmt.Println("    • bisub_vmess.txt")
	}
	if len(configs.ss) > 0 {
		fmt.Println("    • bisub_ss.txt")
	}
	if len(configs.trojan) > 0 {
		fmt.Println("    • bisub_trojan.txt")
	}
	if len(configs.other) > 0 {
		fmt.Println("    • bisub_other.txt")
	}
}

func saveToFile(filename string, configs []string) {
	f, err := os.Create(filename)
	if err != nil {
		fmt.Printf("  ❌ Error creating %s: %v\n", filename, err)
		return
	}
	defer f.Close()

	for _, config := range configs {
		fmt.Fprintf(f, "%s\n", config)
	}

	fmt.Printf("  ✓ %s: %d configs\n", filename, len(configs))
}
