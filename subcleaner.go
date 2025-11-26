// subcleaner_bar.go
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
	"time"

	"github.com/schollz/progressbar/v3"
)

var (
	inFile      = flag.String("in", "subs.txt", "input file with subscription URLs")
	outFile     = flag.String("out", "good.txt", "output file for working configs")
	concurrency = flag.Int("concurrency", 20, "number of concurrent workers")
	timeoutFlag = flag.Duration("timeout", 5*time.Second, "timeout for connection test")
	verbose     = flag.Bool("v", false, "verbose logging")
)

func vlog(format string, a ...interface{}) {
	if *verbose {
		fmt.Printf(format+"\n", a...)
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
	uniq := map[string]struct{}{}
	out := []string{}
	for _, l := range links {
		if _, ok := uniq[l]; ok {
			continue
		}
		uniq[l] = struct{}{}
		out = append(out, l)
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

func testHostPort(ctx context.Context, host, port string, timeout time.Duration) (time.Duration, error) {
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return 0, err
	}
	_ = conn.Close()
	return time.Since(start), nil
}

// TestResult برای پیوند دادن لینک با نتیجه تست
type TestResult struct {
	link    string
	isOk    bool
	latency time.Duration
}

func main() {
	flag.Parse()

	lines, err := readLines(*inFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading input file: %v\n", err)
		os.Exit(1)
	}
	if len(lines) == 0 {
		fmt.Fprintln(os.Stderr, "no URLs found in input file")
		os.Exit(1)
	}
	fmt.Printf("➤ %d subscription URL(s) loaded\n", len(lines))

	// fetch subscriptions
	allLinks := []string{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	fmt.Println("➤ Fetching subscriptions and extracting links...")
	for i, u := range lines {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, urlstr string) {
			defer wg.Done()
			defer func() { <-sem }()
			body, err := fetchURL(urlstr)
			if err != nil {
				fmt.Fprintf(os.Stderr, " ! fetch error (%s): %v\n", urlstr, err)
				return
			}
			links := extractLinks(body)
			mu.Lock()
			allLinks = append(allLinks, links...)
			mu.Unlock()
			fmt.Printf("  • [%02d/%02d] extracted %d links from %s\n", idx+1, len(lines), len(links), urlstr)
		}(i, u)
	}
	wg.Wait()

	// deduplicate
	uniqMap := map[string]struct{}{}
	uniqueLinks := []string{}
	for _, l := range allLinks {
		if l == "" {
			continue
		}
		if _, ok := uniqMap[l]; ok {
			continue
		}
		uniqMap[l] = struct{}{}
		uniqueLinks = append(uniqueLinks, l)
	}
	fmt.Printf("➤ %d unique config links found\n", len(uniqueLinks))
	if len(uniqueLinks) == 0 {
		fmt.Println("No links to test. Exiting.")
		return
	}

	// test connectivity with progress bar
	fmt.Println("➤ Testing connectivity to extracted hosts...")
	jobs := make(chan string, len(uniqueLinks))
	results := make(chan TestResult, len(uniqueLinks))
	ctx := context.Background()

	bar := progressbar.NewOptions(len(uniqueLinks),
		progressbar.OptionSetDescription("Testing links"),
		progressbar.OptionSetWidth(30),
		progressbar.OptionShowCount(),
		progressbar.OptionSetPredictTime(false),
		progressbar.OptionSpinnerType(14),
	)

	var wg2 sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for link := range jobs {
				h, p, err := parseHostPortFromLink(link)
				if err != nil {
					results <- TestResult{link: link, isOk: false, latency: 0}
					bar.Add(1)
					vlog("❌ Failed to parse: %s (%v)", link, err)
					continue
				}
				latency, err := testHostPort(ctx, h, p, *timeoutFlag)
				if err != nil {
					results <- TestResult{link: link, isOk: false, latency: 0}
					vlog("❌ Connection failed: %s:%s (%v)", h, p, err)
				} else {
					results <- TestResult{link: link, isOk: true, latency: latency}
					vlog("✓ Success: %s:%s (latency: %v)", h, p, latency)
				}
				bar.Add(1)
			}
		}()
	}

	for _, l := range uniqueLinks {
		jobs <- l
	}
	close(jobs)

	// جمع‌آوری نتایج موفق
	okLinks := []string{}
	for i := 0; i < len(uniqueLinks); i++ {
		result := <-results
		if result.isOk {
			okLinks = append(okLinks, result.link)
			if *verbose {
				fmt.Printf("  ✓ OK: %s (latency: %v)\n", result.link, result.latency)
			}
		}
	}

	wg2.Wait()
	close(results)

	// save output
	fout, err := os.Create(*outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer fout.Close()
	for _, l := range okLinks {
		_, _ = fout.WriteString(l + "\n")
	}
	fmt.Printf("\n➤ Done. %d/%d links are reachable. Saved to %s\n", len(okLinks), len(uniqueLinks), *outFile)
}
