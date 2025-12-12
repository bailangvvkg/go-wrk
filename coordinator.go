package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	histo "github.com/HdrHistogram/hdrhistogram-go"
	"github.com/tsliwowicz/go-wrk/util"
)

// WorkerStats 从工作节点收集的统计信息
type WorkerStats struct {
	WorkerID     string    `json:"worker_id"`
	Hostname     string    `json:"hostname"`
	NumRequests  int       `json:"num_requests"`
	NumErrs      int       `json:"num_errs"`
	TotRespSize  int64     `json:"tot_resp_size"`
	TotDuration  int64     `json:"tot_duration"` // 纳秒
	ErrMap       map[string]int `json:"err_map"`
	Histogram    string    `json:"histogram"` // 序列化的直方图（JSON字符串）
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
}

// JobRequest 压测任务请求
type JobRequest struct {
	URL                string            `json:"url"`
	Method             string            `json:"method"`
	Headers            map[string]string `json:"headers"`
	Body               string            `json:"body"`
	Duration           int               `json:"duration"`           // 秒
	Goroutines         int               `json:"goroutines"`         // 每个工作节点的goroutine数
	TimeoutMs          int               `json:"timeout_ms"`
	AllowRedirects     bool              `json:"allow_redirects"`
	DisableCompression bool              `json:"disable_compression"`
	DisableKeepAlive   bool              `json:"disable_keep_alive"`
	SkipVerify         bool              `json:"skip_verify"`
	ClientCert         string            `json:"client_cert"`
	ClientKey          string            `json:"client_key"`
	CACert             string            `json:"ca_cert"`
	HTTP2              bool              `json:"http2"`
	WorkerNodes        []string          `json:"worker_nodes"` // 工作节点地址列表
}

// Coordinator 主节点
type Coordinator struct {
	workerStats map[string]*WorkerStats
	statsMutex  sync.RWMutex
	jobRequest  *JobRequest
	results     *AggregatedResults
}

// AggregatedResults 聚合结果
type AggregatedResults struct {
	TotalRequests    int                      `json:"total_requests"`
	TotalErrors      int                      `json:"total_errors"`
	TotalBytes       int64                    `json:"total_bytes"`
	TotalDuration    time.Duration            `json:"total_duration"`
	ErrorDistribution map[string]int           `json:"error_distribution"`
	Histogram        *histo.Histogram         `json:"-"`
	Percentiles      map[string]time.Duration `json:"percentiles"`
	RequestRate      float64                  `json:"request_rate"`
	TransferRate     float64                  `json:"transfer_rate"`
	Workers          int                      `json:"workers"`
	StartTime        time.Time                `json:"start_time"`
	EndTime          time.Time                `json:"end_time"`
}

var (
	coordinatorPort string
	coordinator     *Coordinator
)

func init() {
	flag.StringVar(&coordinatorPort, "port", "8080", "Coordinator HTTP port")
}

func main() {
	flag.Parse()

	coordinator = &Coordinator{
		workerStats: make(map[string]*WorkerStats),
	}

	http.HandleFunc("/start", handleStartJob)
	http.HandleFunc("/status", handleStatus)
	http.HandleFunc("/results", handleResults)
	http.HandleFunc("/worker/report", handleWorkerReport)

	fmt.Printf("Coordinator starting on port %s\n", coordinatorPort)
	log.Fatal(http.ListenAndServe(":"+coordinatorPort, nil))
}

func handleStartJob(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var jobReq JobRequest
	if err := json.Unmarshal(body, &jobReq); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// 验证必要参数
	if jobReq.URL == "" {
		http.Error(w, "URL is required", http.StatusBadRequest)
		return
	}
	if len(jobReq.WorkerNodes) == 0 {
		http.Error(w, "At least one worker node is required", http.StatusBadRequest)
		return
	}

	coordinator.statsMutex.Lock()
	coordinator.workerStats = make(map[string]*WorkerStats)
	coordinator.jobRequest = &jobReq
	coordinator.results = &AggregatedResults{
		ErrorDistribution: make(map[string]int),
		Histogram:         histo.New(1, int64(jobReq.Duration*1000000), 4),
		Percentiles:       make(map[string]time.Duration),
		StartTime:         time.Now(),
		Workers:           len(jobReq.WorkerNodes),
	}
	coordinator.statsMutex.Unlock()

	// 异步分发任务到工作节点
	go func() {
		var wg sync.WaitGroup
		for _, workerAddr := range jobReq.WorkerNodes {
			wg.Add(1)
			go func(addr string) {
				defer wg.Done()
				if err := distributeJobToWorker(addr, jobReq); err != nil {
					log.Printf("Failed to distribute job to worker %s: %v", addr, err)
				}
			}(workerAddr)
		}
		wg.Wait()

		// 所有工作节点任务分发完成后，等待结果收集
		time.Sleep(time.Duration(jobReq.Duration+5) * time.Second) // 等待压测完成 + 缓冲时间
		
		coordinator.statsMutex.Lock()
		coordinator.results.EndTime = time.Now()
		coordinator.aggregateResults()
		coordinator.statsMutex.Unlock()
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "job_started",
		"workers": len(jobReq.WorkerNodes),
		"job_id":  time.Now().Unix(),
	})
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	coordinator.statsMutex.RLock()
	defer coordinator.statsMutex.RUnlock()

	status := map[string]interface{}{
		"workers_total":   len(coordinator.jobRequest.WorkerNodes),
		"workers_reported": len(coordinator.workerStats),
		"job_running":    coordinator.results != nil && coordinator.results.EndTime.IsZero(),
	}

	if coordinator.jobRequest != nil {
		status["target_url"] = coordinator.jobRequest.URL
		status["duration"] = coordinator.jobRequest.Duration
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func handleResults(w http.ResponseWriter, r *http.Request) {
	coordinator.statsMutex.RLock()
	defer coordinator.statsMutex.RUnlock()

	if coordinator.results == nil {
		http.Error(w, "No results available", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(coordinator.results)
}

func handleWorkerReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var stats WorkerStats
	if err := json.Unmarshal(body, &stats); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	coordinator.statsMutex.Lock()
	coordinator.workerStats[stats.WorkerID] = &stats
	coordinator.statsMutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "received"})
}

func distributeJobToWorker(workerAddr string, job JobRequest) error {
	client := &http.Client{Timeout: 10 * time.Second}
	
	// 简化任务，只发送必要参数
	workerJob := map[string]interface{}{
		"url":                job.URL,
		"method":             job.Method,
		"headers":            job.Headers,
		"body":               job.Body,
		"duration":           job.Duration,
		"goroutines":         job.Goroutines,
		"timeout_ms":         job.TimeoutMs,
		"allow_redirects":    job.AllowRedirects,
		"disable_compression": job.DisableCompression,
		"disable_keep_alive": job.DisableKeepAlive,
		"skip_verify":        job.SkipVerify,
		"client_cert":        job.ClientCert,
		"client_key":         job.ClientKey,
		"ca_cert":           job.CACert,
		"http2":             job.HTTP2,
		"coordinator_url":   fmt.Sprintf("http://localhost:%s", coordinatorPort),
	}

	jsonData, err := json.Marshal(workerJob)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s/start", workerAddr)
	resp, err := client.Post(url, "application/json", strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("worker returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *Coordinator) aggregateResults() {
	if c.results == nil || len(c.workerStats) == 0 {
		return
	}

	// 重置结果
	c.results.TotalRequests = 0
	c.results.TotalErrors = 0
	c.results.TotalBytes = 0
	c.results.TotalDuration = 0
	c.results.ErrorDistribution = make(map[string]int)
	c.results.Histogram = histo.New(1, int64(c.jobRequest.Duration*1000000), 4)

	// 汇总所有工作节点的数据
	for _, stats := range c.workerStats {
		c.results.TotalRequests += stats.NumRequests
		c.results.TotalErrors += stats.NumErrs
		c.results.TotalBytes += stats.TotRespSize
		c.results.TotalDuration += time.Duration(stats.TotDuration)

		// 合并错误分布
		for err, count := range stats.ErrMap {
			c.results.ErrorDistribution[err] += count
		}

		// 合并直方图（如果实现了序列化/反序列化）
		// 这里简化处理，实际应该反序列化Histogram字段并合并
	}

	// 计算百分比
	if c.results.Histogram.TotalCount() > 0 {
		percentiles := []float64{10, 50, 75, 90, 95, 99, 99.9, 99.99, 99.999}
		for _, p := range percentiles {
			value := c.results.Histogram.ValueAtPercentile(p)
			c.results.Percentiles[fmt.Sprintf("%.1f", p)] = time.Duration(value * 1000)
		}
	}

	// 计算速率
	testDuration := c.results.EndTime.Sub(c.results.StartTime)
	if testDuration > 0 {
		c.results.RequestRate = float64(c.results.TotalRequests) / testDuration.Seconds()
		c.results.TransferRate = float64(c.results.TotalBytes) / testDuration.Seconds()
	}
}

func printAggregatedResults(results *AggregatedResults) {
	fmt.Printf("\n=== DISTRIBUTED LOAD TEST RESULTS ===\n")
	fmt.Printf("Total Workers:          %d\n", results.Workers)
	fmt.Printf("Test Duration:          %v\n", results.EndTime.Sub(results.StartTime))
	fmt.Printf("Total Requests:         %d\n", results.TotalRequests)
	fmt.Printf("Total Errors:           %d\n", results.TotalErrors)
	fmt.Printf("Total Data Transferred: %v\n", util.ByteSize{float64(results.TotalBytes)})
	fmt.Printf("Request Rate:           %.2f req/sec\n", results.RequestRate)
	fmt.Printf("Transfer Rate:          %v/sec\n", util.ByteSize{results.TransferRate})
	
	if len(results.ErrorDistribution) > 0 {
		fmt.Printf("\nError Distribution:\n")
		var errors []string
		for err := range results.ErrorDistribution {
			errors = append(errors, err)
		}
		sort.Strings(errors)
		for _, err := range errors {
			fmt.Printf("  %s: %d\n", err, results.ErrorDistribution[err])
		}
	}
	
	if len(results.Percentiles) > 0 {
		fmt.Printf("\nLatency Percentiles:\n")
		percentiles := []string{"10.0", "50.0", "75.0", "90.0", "95.0", "99.0", "99.9", "99.99", "99.999"}
		for _, p := range percentiles {
			if dur, ok := results.Percentiles[p]; ok {
				fmt.Printf("  %s%%: %v\n", p, dur)
			}
		}
	}
	fmt.Println("====================================\n")
}
