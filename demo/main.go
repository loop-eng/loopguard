package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	mathrand "math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type claudeEntry struct {
	Type      string    `json:"type"`
	UUID      string    `json:"uuid"`
	RequestID string    `json:"requestId,omitempty"`
	SessionID string    `json:"sessionId"`
	Timestamp string    `json:"timestamp"`
	Message   claudeMsg `json:"message"`
}

type claudeMsg struct {
	Role    string        `json:"role"`
	Model   string        `json:"model,omitempty"`
	Content []interface{} `json:"content"`
	Usage   *claudeUsage  `json:"usage,omitempty"`
}

type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

var scenarios = []string{
	"normal",
	"spin-tool",
	"spin-error",
	"budget-exceed",
	"cost-velocity",
	"stall",
	"interactive",
}

func main() {
	scenario := flag.String("scenario", "normal", fmt.Sprintf("Scenario: %v", scenarios))
	model := flag.String("model", "claude-opus-4-6[1m]", "Model string to use in events")
	speed := flag.Float64("speed", 1.0, "Speed multiplier (2.0 = 2x faster)")
	budget := flag.Float64("budget", 20.0, "Per-session budget for cost calibration")
	projectName := flag.String("project", "loopguard-test", "Project name for session directory")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot determine home directory: %v\n", err)
		os.Exit(1)
	}

	projectDir := filepath.Join(home, ".claude", "projects", "-Users-demo-"+*projectName)
	if err := os.MkdirAll(projectDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create project dir: %v\n", err)
		os.Exit(1)
	}

	sessionID := fmt.Sprintf("demo-%s-%d", *scenario, time.Now().Unix())
	sessionFile := filepath.Join(projectDir, sessionID+".jsonl")

	f, err := os.Create(sessionFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot create session file: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		f.Close()
		fmt.Printf("\nSession file: %s\n", sessionFile)
		fmt.Printf("Session ID:   %s\n", sessionID)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGCONT)

	go func() {
		for s := range sig {
			switch s {
			case syscall.SIGCONT:
				fmt.Println("\n  [SIGCONT received — resumed by LoopGuard!]")
			case os.Interrupt, syscall.SIGTERM:
				fmt.Println("\n  [interrupted]")
				os.Exit(0)
			}
		}
	}()

	fmt.Printf("LoopGuard Demo Simulator\n")
	fmt.Printf("========================\n")
	fmt.Printf("Scenario:    %s\n", *scenario)
	fmt.Printf("Session:     %s\n", sessionID)
	fmt.Printf("File:        %s\n", sessionFile)
	fmt.Printf("Model:       %s\n", *model)
	fmt.Printf("Speed:       %.1fx\n", *speed)
	fmt.Printf("PID:         %d\n", os.Getpid())
	fmt.Printf("\nStart loopguard in another terminal, then watch it react.\n\n")

	enc := json.NewEncoder(f)
	delay := func(base time.Duration) {
		time.Sleep(time.Duration(float64(base) / *speed))
	}

	switch *scenario {
	case "normal":
		runNormal(enc, sessionID, *model, delay)
	case "spin-tool":
		runSpinTool(enc, sessionID, *model, delay)
	case "spin-error":
		runSpinError(enc, sessionID, *model, delay)
	case "budget-exceed":
		runBudgetExceed(enc, sessionID, *model, delay, *budget)
	case "cost-velocity":
		runCostVelocity(enc, sessionID, *model, delay)
	case "stall":
		runStall(enc, sessionID, *model, delay)
	case "interactive":
		runInteractive(enc, sessionID, *model, delay)
	default:
		fmt.Fprintf(os.Stderr, "unknown scenario: %s\nAvailable: %v\n", *scenario, scenarios)
		os.Exit(1)
	}

	fmt.Println("\nSimulation complete.")
}

func writeAssistantEventAt(enc *json.Encoder, sessionID, model, requestID string, content []interface{}, usage *claudeUsage, ts time.Time) {
	entry := claudeEntry{
		Type:      "assistant",
		UUID:      randomUUID(),
		RequestID: requestID,
		SessionID: sessionID,
		Timestamp: ts.Format(time.RFC3339Nano),
		Message: claudeMsg{
			Role:    "assistant",
			Model:   model,
			Content: content,
			Usage:   usage,
		},
	}
	_ = enc.Encode(entry)
}

func writeAssistantEvent(enc *json.Encoder, sessionID, model, requestID string, content []interface{}, usage *claudeUsage) {
	writeAssistantEventAt(enc, sessionID, model, requestID, content, usage, time.Now())
}

func writeUserEventAt(enc *json.Encoder, sessionID string, content []interface{}, ts time.Time) {
	entry := claudeEntry{
		Type:      "user",
		UUID:      randomUUID(),
		SessionID: sessionID,
		Timestamp: ts.Format(time.RFC3339Nano),
		Message: claudeMsg{
			Role:    "user",
			Content: content,
		},
	}
	_ = enc.Encode(entry)
}

func writeUserEvent(enc *json.Encoder, sessionID string, content []interface{}) {
	writeUserEventAt(enc, sessionID, content, time.Now())
}

func toolUseContent(name, id string, input map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"type":  "tool_use",
		"name":  name,
		"id":    id,
		"input": input,
	}
}

func toolResultContent(toolUseID string, output string, isError bool) map[string]interface{} {
	return map[string]interface{}{
		"type":        "tool_result",
		"tool_use_id": toolUseID,
		"content":     output,
		"is_error":    isError,
	}
}

func runNormal(enc *json.Encoder, sessionID, model string, delay func(time.Duration)) {
	fmt.Println("[normal] Simulating healthy agent session...")

	tools := []struct {
		name  string
		input map[string]interface{}
	}{
		{"Read", map[string]interface{}{"file_path": "/src/main.go"}},
		{"Read", map[string]interface{}{"file_path": "/src/handler.go"}},
		{"Edit", map[string]interface{}{"file_path": "/src/handler.go", "old_string": "func old()", "new_string": "func new()"}},
		{"Bash", map[string]interface{}{"command": "go test ./..."}},
		{"Read", map[string]interface{}{"file_path": "/src/config.go"}},
		{"Write", map[string]interface{}{"file_path": "/src/utils.go", "content": "package main"}},
		{"Bash", map[string]interface{}{"command": "go build ./..."}},
	}

	for i, tool := range tools {
		reqID := fmt.Sprintf("req_%d_%d", i, time.Now().UnixNano())
		toolID := fmt.Sprintf("tool_%d", i)

		usage := &claudeUsage{
			InputTokens:  5000 + mathrand.Intn(10000),
			OutputTokens: 200 + mathrand.Intn(500),
		}

		writeAssistantEvent(enc, sessionID, model, reqID, []interface{}{
			toolUseContent(tool.name, toolID, tool.input),
		}, usage)
		fmt.Printf("  [%d/%d] %s\n", i+1, len(tools), tool.name)
		delay(2 * time.Second)

		writeUserEvent(enc, sessionID, []interface{}{
			toolResultContent(toolID, "ok", false),
		})
		delay(1 * time.Second)
	}

	fmt.Println("[normal] Session completed without triggering any alerts.")
}

func runSpinTool(enc *json.Encoder, sessionID, model string, delay func(time.Duration)) {
	fmt.Println("[spin-tool] Simulating repeated tool call spin...")
	fmt.Println("  Expected: LoopGuard detects spin after 3 identical calls.")

	for i := 0; i < 10; i++ {
		reqID := fmt.Sprintf("req_spin_%d_%d", i, time.Now().UnixNano())
		toolID := fmt.Sprintf("tool_spin_%d", i)

		writeAssistantEvent(enc, sessionID, model, reqID, []interface{}{
			toolUseContent("Bash", toolID, map[string]interface{}{"command": "npm test"}),
		}, &claudeUsage{InputTokens: 8000, OutputTokens: 300})
		fmt.Printf("  [%d/10] Bash {command: npm test} (identical)\n", i+1)
		delay(1 * time.Second)

		writeUserEvent(enc, sessionID, []interface{}{
			toolResultContent(toolID, "FAIL Tests: 3 failed, 12 passed", false),
		})
		delay(500 * time.Millisecond)
	}
}

func runSpinError(enc *json.Encoder, sessionID, model string, delay func(time.Duration)) {
	fmt.Println("[spin-error] Simulating error echo spin...")
	fmt.Println("  Expected: LoopGuard detects repeated identical errors after 3.")

	for i := 0; i < 8; i++ {
		reqID := fmt.Sprintf("req_err_%d_%d", i, time.Now().UnixNano())
		toolID := fmt.Sprintf("tool_err_%d", i)

		writeAssistantEvent(enc, sessionID, model, reqID, []interface{}{
			toolUseContent("Edit", toolID, map[string]interface{}{
				"file_path":  "/src/app.js",
				"old_string": fmt.Sprintf("line_%d", i),
				"new_string": fmt.Sprintf("fixed_%d", i),
			}),
		}, &claudeUsage{InputTokens: 6000, OutputTokens: 400})
		fmt.Printf("  [%d/8] Edit attempt\n", i+1)
		delay(1 * time.Second)

		writeUserEvent(enc, sessionID, []interface{}{
			toolResultContent(toolID, "TypeError: Cannot read property 'foo' of undefined", true),
		})
		delay(500 * time.Millisecond)
	}
}

func runBudgetExceed(enc *json.Encoder, sessionID, model string, delay func(time.Duration), budgetLimit float64) {
	fmt.Printf("[budget-exceed] Simulating budget breach (limit: $%.2f)...\n", budgetLimit)
	fmt.Println("  Expected: warning at 80%, pause at 100%.")

	costPerCall := budgetLimit / 8
	tokensForCost := int(costPerCall * 1_000_000 / 25.0)

	for i := 0; i < 15; i++ {
		reqID := fmt.Sprintf("req_budget_%d_%d", i, time.Now().UnixNano())
		toolID := fmt.Sprintf("tool_budget_%d", i)

		writeAssistantEvent(enc, sessionID, model, reqID, []interface{}{
			toolUseContent("Read", toolID, map[string]interface{}{
				"file_path": fmt.Sprintf("/src/module_%d.go", i),
			}),
		}, &claudeUsage{
			InputTokens:  50000,
			OutputTokens: tokensForCost,
		})

		estCost := float64(i+1) * costPerCall
		pct := (estCost / budgetLimit) * 100
		fmt.Printf("  [%d/15] Read module_%d.go  (est. $%.2f / $%.2f = %.0f%%)\n",
			i+1, i, estCost, budgetLimit, pct)
		delay(2 * time.Second)

		writeUserEvent(enc, sessionID, []interface{}{
			toolResultContent(toolID, "package main\n\nfunc init() { /* ... */ }", false),
		})
		delay(1 * time.Second)
	}
}

func runCostVelocity(enc *json.Encoder, sessionID, model string, delay func(time.Duration)) {
	fmt.Println("[cost-velocity] Simulating high cost velocity ($5/min)...")
	fmt.Println("  Expected: LoopGuard detects cost velocity exceeding $2/min threshold.")
	fmt.Println("  Uses simulated timestamps (5s apart) so velocity check works at any speed.")

	simTime := time.Now()

	for i := 0; i < 20; i++ {
		reqID := fmt.Sprintf("req_vel_%d_%d", i, time.Now().UnixNano())
		toolID := fmt.Sprintf("tool_vel_%d", i)

		simTime = simTime.Add(5 * time.Second)

		writeAssistantEventAt(enc, sessionID, model, reqID, []interface{}{
			toolUseContent("Bash", toolID, map[string]interface{}{
				"command": fmt.Sprintf("process-large-file-%d.sh", i),
			}),
		}, &claudeUsage{
			InputTokens:  200000,
			OutputTokens: 10000,
		}, simTime)
		fmt.Printf("  [%d/20] Large token call (200K in + 10K out)\n", i+1)
		delay(1 * time.Second)

		simTime = simTime.Add(1 * time.Second)
		writeUserEventAt(enc, sessionID, []interface{}{
			toolResultContent(toolID, "processed", false),
		}, simTime)
		delay(500 * time.Millisecond)
	}
}

func runStall(enc *json.Encoder, sessionID, model string, delay func(time.Duration)) {
	fmt.Println("[stall] Simulating no-progress stall...")
	fmt.Println("  Expected: LoopGuard warns after 10 minutes of no file modifications.")

	reqID := fmt.Sprintf("req_edit_%d", time.Now().UnixNano())
	toolID := "tool_edit_0"
	writeAssistantEvent(enc, sessionID, model, reqID, []interface{}{
		toolUseContent("Edit", toolID, map[string]interface{}{
			"file_path":  "/src/main.go",
			"old_string": "old",
			"new_string": "new",
		}),
	}, &claudeUsage{InputTokens: 5000, OutputTokens: 200})
	fmt.Println("  [1] Edit /src/main.go (sets lastFileEdit)")
	delay(1 * time.Second)

	writeUserEvent(enc, sessionID, []interface{}{
		toolResultContent(toolID, "ok", false),
	})
	delay(1 * time.Second)

	for i := 0; i < 24; i++ {
		reqID := fmt.Sprintf("req_stall_%d_%d", i, time.Now().UnixNano())
		toolID := fmt.Sprintf("tool_stall_%d", i)

		writeAssistantEvent(enc, sessionID, model, reqID, []interface{}{
			toolUseContent("Read", toolID, map[string]interface{}{
				"file_path": fmt.Sprintf("/src/file_%d.go", i),
			}),
		}, &claudeUsage{InputTokens: 5000, OutputTokens: 100})
		fmt.Printf("  [%d/24] Read file_%d.go (no file modifications)\n", i+2, i)
		delay(30 * time.Second)

		writeUserEvent(enc, sessionID, []interface{}{
			toolResultContent(toolID, "package main", false),
		})
		delay(500 * time.Millisecond)
	}
}

func runInteractive(enc *json.Encoder, sessionID, model string, delay func(time.Duration)) {
	fmt.Println("[interactive] Running as a long-lived agent process.")
	fmt.Println("  This process stays alive so LoopGuard can discover it by PID.")
	fmt.Println("  It writes events every 5 seconds. Use Ctrl+C to stop.")
	fmt.Printf("  PID: %d\n\n", os.Getpid())

	fmt.Println("  Try these while this runs:")
	fmt.Println("    loopguard status            — see this session")
	fmt.Println("    loopguard resume <id>       — resume after pause")
	fmt.Println("")

	iteration := 0
	for {
		iteration++
		reqID := fmt.Sprintf("req_int_%d_%d", iteration, time.Now().UnixNano())
		toolID := fmt.Sprintf("tool_int_%d", iteration)

		var toolName string
		var input map[string]interface{}
		var usage *claudeUsage

		switch {
		case iteration%7 == 0:
			toolName = "Edit"
			input = map[string]interface{}{
				"file_path":  fmt.Sprintf("/src/file_%d.go", iteration),
				"old_string": "old",
				"new_string": "new",
			}
			usage = &claudeUsage{InputTokens: 8000, OutputTokens: 500}
		case iteration%3 == 0:
			toolName = "Bash"
			input = map[string]interface{}{"command": fmt.Sprintf("go test ./pkg%d/...", iteration)}
			usage = &claudeUsage{InputTokens: 10000, OutputTokens: 800}
		default:
			toolName = "Read"
			input = map[string]interface{}{"file_path": fmt.Sprintf("/src/module_%d.go", iteration)}
			usage = &claudeUsage{InputTokens: 5000 + mathrand.Intn(5000), OutputTokens: 200 + mathrand.Intn(300)}
		}

		writeAssistantEvent(enc, sessionID, model, reqID, []interface{}{
			toolUseContent(toolName, toolID, input),
		}, usage)

		cost := float64(usage.InputTokens)*5.0/1e6 + float64(usage.OutputTokens)*25.0/1e6
		fmt.Printf("  [iter %d] %s ($%.4f)\n", iteration, toolName, cost)

		delay(3 * time.Second)

		writeUserEvent(enc, sessionID, []interface{}{
			toolResultContent(toolID, "ok", false),
		})
		delay(2 * time.Second)
	}
}

func randomUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
