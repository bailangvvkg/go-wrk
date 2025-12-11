package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	histo "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/tsliwowicz/go-wrk/loader"
	"github.com/tsliwowicz/go-wrk/util"
)

// WorkerConfig 工作节点配置
type WorkerConfig struct {
	URL                string            `json:"url"`
	Method             string            `json:"method"`
	Headers            map[string]string `json:"headers"`
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
	WorkerID           string            `json:"worker_id"`
}

// Worker 工作节点
type Worker struct {
	config      *WorkerConfig
	stats       *loader.RequesterStats
	statsMutex  sync.RWMutex
	isRunning   bool
	hostname    string
}

var (
	workerPort string
	workerID   string
	worker     *Worker
)

func init() {
	flag.StringVar(&workerPort, "port", "8081", "Worker HTTP port")
	flag.StringVar(&workerID, "id", "", "Worker ID (default: hostname)")
}

func main() {
	flag.Parse()

	// 获取主机名作为默认worker ID
	hostname, err := os.Hostname()
	if err != nil {
		hostname = fmt.Sprintf("worker-%d", time.Now().Unix())
	}
	if workerID == "" {
		workerID = hostname
	}

	worker = &Worker{
		hostname: hostname,
	}

	http.HandleFunc("/start", handleStartLoadTest)
	http.HandleFunc("/stop", handleStopLoadTest)
	http.HandleFunc("/status", handleWorkerStatus)
	http.HandleFunc("/health", handleHealthCheck)

	fmt.Printf("Worker %s starting on port %s\n", workerID, workerPort)
	log.Fatal(http.ListenAndServe(":"+workerPort, nil))
}

func handleStartLoadTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	worker.statsMutex.Lock()
	if worker.isRunning {
		worker.statsMutex.Unlock()
		http.Error(w, "Worker is already running a load test", http.StatusConflict)
		return
	}
	worker.isRunning = true
	worker.statsMutex.Unlock()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		worker.statsMutex.Lock()
		worker.isRunning = false
		worker.statsMutex.Unlock()
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var config WorkerConfig
	if err := json.Unmarshal(body, &config); err != nil {
		worker.statsMutex.Lock()
		worker.isRunning = false
		worker.statsMutex.Unlock()
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// 设置worker ID
	config.WorkerID = workerID
	if config.CoordinatorURL == "" {
		config.CoordinatorURL = "http://localhost:8080"
	}

	worker.config = &config

	// 异步执行压测
	go func() {
		workerStats := runLoadTest(config)
		
		// 报告结果给协调器
		if err := reportResultsToCoordinator(config.CoordinatorURL, workerStats); err != nil {
			log.Printf("Failed to report results to coordinator: %v", err)
		}

		worker.statsMutex.Lock()
		worker.isRunning = false
		worker.stats = workerStats
		worker.statsMutex.Unlock()
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "load_test_started",
		"worker_id": workerID,
		"duration": config.Duration,
		"goroutines": config.Goroutines,
	})
}

func handleStopLoadTest(w http.ResponseWriter, r *http.Request) {
	// 目前简化处理，实际应该停止正在运行的压测
	worker.statsMutex.Lock()
	worker.isRunning = false
	worker.statsMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "stopped",
		"worker_id": workerID,
	})
}

func handleWorkerStatus(w http.ResponseWriter, r *http.Request) {
	worker.statsMutex.RLock()
	defer worker.statsMutex.RUnlock()

	status := map[string]interface{}{
		"worker_id":  workerID,
		"hostname":   worker.hostname,
		"is_running": worker.isRunning,
		"port":       workerPort,
	}

	if worker.config != nil {
		status["target_url"] = worker.config.URL
		status["duration"] = worker.config.Duration
		status["goroutines"] = worker.config.Goroutines
	}

	if worker.stats != nil {
		status["last_test_requests"] = worker.stats.NumRequests
		status["last_test_errors"] = worker.stats.NumErrs
		status["last_test_bytes"] = worker.stats.TotRespSize
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"worker_id": workerID,
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

func runLoadTest(config WorkerConfig) *loader.RequesterStats {
	fmt.Printf("Worker %s starting load test on %s for %d seconds with %d goroutines\n",
		workerID, config.URL, config.Duration, config.Goroutines)

	// 创建统计信息收集器
	statsAggregator := make(chan *loader.RequesterStats, config.Goroutines)

	// 创建负载配置
	loadCfg := loader.NewLoadCfg(
		config.Duration,
		config.Goroutines,
		config.URL,
		config.Body,
		config.Method,
		"", // host header
		config.Headers,
		statsAggregator,
		config.TimeoutMs,
		config.AllowRedirects,
		config.DisableCompression,
		config.DisableKeepAlive,
		config.SkipVerify,
		config.ClientCert,
		config.ClientKey,
		config.CACert,
		config.HTTP2,
	)

	startTime := time.Now()

	// 启动多个goroutine进行压测
	for i := 0; i < config.Goroutines; i++ {
		go loadCfg.RunSingleLoadSession()
	}

	// 收集结果
	responders := 0
	aggStats := &loader.RequesterStats{
		ErrMap:    make(map[string]int),
		Histogram: histo.New(1, int64(config.Duration*1000000), 4),
	}

	for responders < config.Goroutines {
		stats := <-statsAggregator
		aggStats.NumErrs += stats.NumErrs
		aggStats.NumRequests += stats.NumRequests
		aggStats.TotRespSize += stats.TotRespSize
		aggStats.TotDuration += stats.TotDuration
		responders++

		for k, v := range stats.ErrMap {
			aggStats.ErrMap[k] += v
		}
		aggStats.Histogram.Merge(stats.Histogram)
	}

	endTime := time.Now()

	// 打印本地结果
	fmt.Printf("\n=== Worker %s Results ===\n", workerID)
	fmt.Printf("Test Duration:   %v\n", endTime.Sub(startTime))
	fmt.Printf("Requests:        %d\n", aggStats.NumRequests)
	fmt.Printf("Errors:          %d\n", aggStats.NumErrs)
	fmt.Printf("Data Transferred: %v\n", util.ByteSize{float64(aggStats.TotRespSize)})
	
	if aggStats.NumRequests > 0 {
		reqRate := float64(aggStats.NumRequests) / endTime.Sub(startTime).Seconds()
		bytesRate := float64(aggStats.TotRespSize) / endTime.Sub(startTime).Seconds()
		fmt.Printf("Request Rate:    %.2f req/sec\n", reqRate)
		fmt.Printf("Transfer Rate:   %v/sec\n", util.ByteSize{bytesRate})
	}
	fmt.Println("=========================\n")

	return aggStats
}

func reportResultsToCoordinator(coordinatorURL string, stats *loader.RequesterStats) error {
	// 准备要报告的数据
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = workerID
	}

	// 序列化直方图（简化处理，实际应该实现完整的序列化）
	histogramData, _ := json.Marshal(map[string]interface{}{
		"total_count": stats.Histogram.TotalCount(),
		"min_value":   stats.Histogram.Min(),
		"max_value":   stats.Histogram.Max(),
		"mean":        stats.Histogram.Mean(),
		"stddev":      stats.Histogram.StdDev(),
	})

	workerStats := map[string]interface{}{
		"worker_id":    workerID,
		"hostname":     hostname,
		"num_requests": stats.NumRequests,
		"num_errs":     stats.NumErrs,
		"tot_resp_size": stats.TotRespSize,
		"tot_duration": stats.TotDuration.Nanoseconds(),
		"err_map":      stats.ErrMap,
		"histogram":    string(histogramData),
		"start_time":   time.Now().Add(-stats.TotDuration).Format(time.RFC3339),
		"end_time":     time.Now().Format(time.RFC3339),
	}

	jsonData, err := json.Marshal(workerStats)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("%s/worker/report", coordinatorURL)
	resp, err := client.Post(url, "application/json", io.NopCloser(strings.NewReader(string(jsonData))))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("coordinator returned status %d: %s", resp.StatusCode, string(body))
	}

	fmt.Printf("Results reported to coordinator at %s\n", coordinatorURL)
	return nil
}

// 辅助函数：将字符串转换为io.Reader
func stringsNewReader(s string) io.Reader {
	return strings.NewReader(s)
}

var strings = struct {
	NewReader func(string) io.Reader
}{
	NewReader: strings.NewReader,
}
