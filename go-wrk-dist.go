package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tsliwowicz/go-wrk/util"
)

// DistLoadTestConfig 分布式压测配置
type DistLoadTestConfig struct {
	URL                string            `json:"url"`
	Method             string            `json:"method"`
	Headers            []string          `json:"-"`
	HeaderMap          map[string]string `json:"headers"`
	Body               string            `json:"body"`
	Duration           int               `json:"duration"`
	Goroutines         int               `json:"goroutines"`
	TimeoutMs          int               `json:"timeout_ms"`
	AllowRedirects     bool              `json:"allow_redirects"`
	DisableCompression bool              `json:"disable_compression"`
	DisableKeepAlive   bool              `json:"disable_keep_alive"`
	SkipVerify         bool              `json:"skip_verify"`
	ClientCert         string            `json:"client_cert"`
	ClientKey          string            `json:"client_key"`
	CACert             string            `json:"ca_cert"`
	HTTP2              bool              `json:"http2"`
	CoordinatorURL     string            `json:"coordinator_url"`
	WorkerNodes        []string          `json:"worker_nodes"`
}

var (
	distConfig DistLoadTestConfig
	headerFlags util.HeaderList
)

func init() {
	// 继承go-wrk的参数
	flag.BoolVar(&distConfig.AllowRedirects, "redir", false, "Allow Redirects")
	flag.BoolVar(&distConfig.DisableCompression, "no-c", false, "Disable Compression")
	flag.BoolVar(&distConfig.DisableKeepAlive, "no-ka", false, "Disable KeepAlive")
	flag.BoolVar(&distConfig.SkipVerify, "no-vr", false, "Skip verifying SSL certificate")
	flag.IntVar(&distConfig.Goroutines, "c", 10, "Number of goroutines per worker")
	flag.IntVar(&distConfig.Duration, "d", 10, "Duration of test in seconds")
	flag.IntVar(&distConfig.TimeoutMs, "T", 1000, "Socket/request timeout in ms")
	flag.StringVar(&distConfig.Method, "M", "GET", "HTTP method")
	flag.StringVar(&distConfig.Body, "body", "", "request body string or @filename")
	flag.StringVar(&distConfig.ClientCert, "cert", "", "CA certificate file")
	flag.StringVar(&distConfig.ClientKey, "key", "", "Private key file name")
	flag.StringVar(&distConfig.CACert, "ca", "", "CA file to verify peer against")
	flag.BoolVar(&distConfig.HTTP2, "http", true, "Use HTTP/2")
	flag.Var(&headerFlags, "H", "Header to add to each request")

	// 分布式特有参数
	flag.StringVar(&distConfig.CoordinatorURL, "coordinator", "http://localhost:8080", "Coordinator URL")
	workers := flag.String("workers", "localhost:8081", "Comma-separated list of worker nodes (host:port)")
	
	// 解析参数
	flag.Parse()
	
	// 解析worker节点
	distConfig.WorkerNodes = strings.Split(*workers, ",")
	for i, worker := range distConfig.WorkerNodes {
		distConfig.WorkerNodes[i] = strings.TrimSpace(worker)
	}
	
	// 获取URL（最后一个参数）
	args := flag.Args()
	if len(args) > 0 {
		distConfig.URL = args[0]
	}
	
	// 解析headers
	distConfig.HeaderMap = make(map[string]string)
	for _, hdr := range headerFlags {
		hp := strings.SplitN(hdr, ":", 2)
		if len(hp) == 2 {
			distConfig.HeaderMap[strings.TrimSpace(hp[0])] = strings.TrimSpace(hp[1])
		}
	}
}

func main() {
	if distConfig.URL == "" {
		printDistUsage()
		return
	}

	if len(distConfig.WorkerNodes) == 0 {
		fmt.Println("Error: At least one worker node is required")
		printDistUsage()
		return
	}

	fmt.Printf("Starting distributed load test with %d worker(s)\n", len(distConfig.WorkerNodes))
	fmt.Printf("Target URL: %s\n", distConfig.URL)
	fmt.Printf("Duration: %d seconds\n", distConfig.Duration)
	fmt.Printf("Goroutines per worker: %d\n", distConfig.Goroutines)
	fmt.Printf("Total concurrent connections: %d\n", distConfig.Goroutines*len(distConfig.WorkerNodes))
	fmt.Println()

	// 发送请求到协调器
	if err := startDistributedTest(); err != nil {
		fmt.Printf("Error starting distributed test: %v\n", err)
		os.Exit(1)
	}

	// 等待测试完成并获取结果
	time.Sleep(time.Duration(distConfig.Duration+2) * time.Second)
	if err := getAndDisplayResults(); err != nil {
		fmt.Printf("Error getting results: %v\n", err)
		os.Exit(1)
	}
}

func startDistributedTest() error {
	// 准备请求数据
	jobRequest := map[string]interface{}{
		"url":                distConfig.URL,
		"method":             distConfig.Method,
		"headers":            distConfig.HeaderMap,
		"body":               distConfig.Body,
		"duration":           distConfig.Duration,
		"goroutines":         distConfig.Goroutines,
		"timeout_ms":         distConfig.TimeoutMs,
		"allow_redirects":    distConfig.AllowRedirects,
		"disable_compression": distConfig.DisableCompression,
		"disable_keep_alive": distConfig.DisableKeepAlive,
		"skip_verify":        distConfig.SkipVerify,
		"client_cert":        distConfig.ClientCert,
		"client_key":         distConfig.ClientKey,
		"ca_cert":           distConfig.CACert,
		"http2":             distConfig.HTTP2,
		"worker_nodes":      distConfig.WorkerNodes,
	}

	jsonData, err := json.Marshal(jobRequest)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 10 * time.Second}
	url := fmt.Sprintf("%s/start", distConfig.CoordinatorURL)
	resp, err := client.Post(url, "application/json", strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("coordinator returned status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	fmt.Printf("Job started successfully: %v\n", result)
	fmt.Println("Waiting for test to complete...")
	return nil
}

func getAndDisplayResults() error {
	// 获取状态
	client := &http.Client{Timeout: 10 * time.Second}
	statusURL := fmt.Sprintf("%s/status", distConfig.CoordinatorURL)
	resp, err := client.Get(statusURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var status map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return err
	}

	fmt.Printf("\nTest Status: %v\n", status)

	// 获取结果
	resultsURL := fmt.Sprintf("%s/results", distConfig.CoordinatorURL)
	resp, err = client.Get(resultsURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var results map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return err
	}

	displayResults(results)
	return nil
}

func displayResults(results map[string]interface{}) {
	fmt.Println("\n=== DISTRIBUTED LOAD TEST FINAL RESULTS ===")
	
	if totalWorkers, ok := results["workers"].(float64); ok {
		fmt.Printf("Total Workers:          %.0f\n", totalWorkers)
	}
	
	if startTimeStr, ok := results["start_time"].(string); ok {
		if endTimeStr, ok := results["end_time"].(string); ok {
			startTime, _ := time.Parse(time.RFC3339, startTimeStr)
			endTime, _ := time.Parse(time.RFC3339, endTimeStr)
			duration := endTime.Sub(startTime)
			fmt.Printf("Test Duration:          %v\n", duration)
		}
	}
	
	if totalRequests, ok := results["total_requests"].(float64); ok {
		fmt.Printf("Total Requests:         %.0f\n", totalRequests)
	}
	
	if totalErrors, ok := results["total_errors"].(float64); ok {
		fmt.Printf("Total Errors:           %.0f\n", totalErrors)
	}
	
	if totalBytes, ok := results["total_bytes"].(float64); ok {
		fmt.Printf("Total Data Transferred: %v\n", util.ByteSize{totalBytes})
	}
	
	if requestRate, ok := results["request_rate"].(float64); ok {
		fmt.Printf("Request Rate:           %.2f req/sec\n", requestRate)
	}
	
	if transferRate, ok := results["transfer_rate"].(float64); ok {
		fmt.Printf("Transfer Rate:          %v/sec\n", util.ByteSize{transferRate})
	}
	
	if errorDist, ok := results["error_distribution"].(map[string]interface{}); ok && len(errorDist) > 0 {
		fmt.Println("\nError Distribution:")
		for err, count := range errorDist {
			fmt.Printf("  %s: %.0f\n", err, count.(float64))
		}
	}
	
	if percentiles, ok := results["percentiles"].(map[string]interface{}); ok && len(percentiles) > 0 {
		fmt.Println("\nLatency Percentiles:")
		percentileKeys := []string{"10.0", "50.0", "75.0", "90.0", "95.0", "99.0", "99.9", "99.99", "99.999"}
		for _, key := range percentileKeys {
			if value, ok := percentiles[key]; ok {
				if durStr, ok := value.(string); ok {
					fmt.Printf("  %s%%: %s\n", key, durStr)
				}
			}
		}
	}
	
	fmt.Println("===========================================\n")
}

func printDistUsage() {
	fmt.Println("Usage: go-wrk-dist <options> <url>")
	fmt.Println("Distributed load testing tool based on go-wrk")
	fmt.Println()
	fmt.Println("Standard go-wrk options:")
	fmt.Println("  -c        Number of goroutines per worker (default 10)")
	fmt.Println("  -d        Duration of test in seconds (default 10)")
	fmt.Println("  -H        Header to add to each request (can be used multiple times)")
	fmt.Println("  -M        HTTP method (default GET)")
	fmt.Println("  -T        Socket/request timeout in ms (default 1000)")
	fmt.Println("  -body     Request body string or @filename")
	fmt.Println("  -redir    Allow Redirects (default false)")
	fmt.Println("  -no-c     Disable Compression")
	fmt.Println("  -no-ka    Disable KeepAlive")
	fmt.Println("  -no-vr    Skip verifying SSL certificate")
	fmt.Println("  -cert     CA certificate file")
	fmt.Println("  -key      Private key file name")
	fmt.Println("  -ca       CA file to verify peer against")
	fmt.Println("  -http     Use HTTP/2 (default true)")
	fmt.Println()
	fmt.Println("Distributed options:")
	fmt.Println("  -coordinator Coordinator URL (default http://localhost:8080)")
	fmt.Println("  -workers    Comma-separated list of worker nodes (host:port)")
	fmt.Println("              (default localhost:8081)")
	fmt.Println()
	fmt.Println("Example:")
	fmt.Println("  go-wrk-dist -c 100 -d 30 -workers '192.168.1.100:8081,192.168.1.101:8081' http://example.com")
	fmt.Println("  go-wrk-dist -c 50 -d 60 -coordinator http://coordinator:8080 \\")
	fmt.Println("              -workers 'worker1:8081,worker2:8081,worker3:8081' \\")
	fmt.Println("              -H 'Content-Type: application/json' \\")
	fmt.Println("              -body '{\"test\":\"data\"}' \\")
	fmt.Println("              -M POST https://api.example.com/test")
}
