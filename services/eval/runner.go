package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
	runtimev1 "github.com/instantcocoa/delos/gen/go/runtime/v1"
)

// Runner executes evaluation runs by coordinating with runtime, prompt, and datasets services.
type Runner struct {
	logger         *slog.Logger
	store          Store
	runtimeClient  runtimev1.RuntimeServiceClient
	promptClient   promptv1.PromptServiceClient
	datasetsClient datasetsv1.DatasetsServiceClient

	// Configuration
	pollInterval time.Duration
	concurrency  int

	// Control
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// RunnerConfig contains configuration for the eval runner.
type RunnerConfig struct {
	PollInterval time.Duration
	Concurrency  int
}

// NewRunner creates a new eval runner.
func NewRunner(
	logger *slog.Logger,
	store Store,
	runtimeClient runtimev1.RuntimeServiceClient,
	promptClient promptv1.PromptServiceClient,
	datasetsClient datasetsv1.DatasetsServiceClient,
	cfg RunnerConfig,
) *Runner {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.Concurrency == 0 {
		cfg.Concurrency = 2
	}

	return &Runner{
		logger:         logger.With("component", "runner"),
		store:          store,
		runtimeClient:  runtimeClient,
		promptClient:   promptClient,
		datasetsClient: datasetsClient,
		pollInterval:   cfg.PollInterval,
		concurrency:    cfg.Concurrency,
		stopCh:         make(chan struct{}),
	}
}

// Start begins processing pending eval runs.
func (r *Runner) Start(ctx context.Context) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.runLoop(ctx)
	}()
	r.logger.Info("eval runner started", "poll_interval", r.pollInterval, "concurrency", r.concurrency)
}

// Stop gracefully stops the runner.
func (r *Runner) Stop() {
	close(r.stopCh)
	r.wg.Wait()
	r.logger.Info("eval runner stopped")
}

func (r *Runner) runLoop(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.processPendingRuns(ctx)
		}
	}
}

func (r *Runner) processPendingRuns(ctx context.Context) {
	// Find pending runs
	runs, _, err := r.store.ListEvalRuns(ctx, ListEvalRunsQuery{
		Status: EvalRunStatusPending,
		Limit:  r.concurrency,
	})
	if err != nil {
		r.logger.Error("failed to list pending runs", "error", err)
		return
	}

	for _, run := range runs {
		r.wg.Add(1)
		go func(run *EvalRun) {
			defer r.wg.Done()
			r.executeRun(ctx, run)
		}(run)
	}
}

func (r *Runner) executeRun(ctx context.Context, run *EvalRun) {
	r.logger.Info("executing eval run", "id", run.ID, "name", run.Name)

	// Mark as running
	now := time.Now()
	run.Status = EvalRunStatusRunning
	run.StartedAt = &now
	if err := r.store.UpdateEvalRun(ctx, run); err != nil {
		r.logger.Error("failed to update run status", "id", run.ID, "error", err)
		return
	}

	// Execute and handle errors
	err := r.doExecute(ctx, run)
	if err != nil {
		r.logger.Error("eval run failed", "id", run.ID, "error", err)
		run.Status = EvalRunStatusFailed
		run.ErrorMessage = err.Error()
	} else {
		run.Status = EvalRunStatusCompleted
	}

	// Mark as completed
	completedAt := time.Now()
	run.CompletedAt = &completedAt
	if err := r.store.UpdateEvalRun(ctx, run); err != nil {
		r.logger.Error("failed to update run completion", "id", run.ID, "error", err)
	}

	r.logger.Info("eval run finished", "id", run.ID, "status", run.Status)
}

func (r *Runner) doExecute(ctx context.Context, run *EvalRun) error {
	// Step 1: Fetch prompt (use ID directly - versioning handled at run creation time)
	promptResp, err := r.promptClient.GetPrompt(ctx, &promptv1.GetPromptRequest{
		Id: run.PromptID,
	})
	if err != nil {
		return fmt.Errorf("failed to fetch prompt: %w", err)
	}
	if promptResp.Prompt == nil {
		return fmt.Errorf("prompt not found: %s", run.PromptID)
	}
	prompt := promptResp.Prompt

	// Step 2: Fetch examples from dataset
	examplesResp, err := r.datasetsClient.GetExamples(ctx, &datasetsv1.GetExamplesRequest{
		DatasetId: run.DatasetID,
		Limit:     1000, // TODO: use sampling config
		Shuffle:   run.Config.Shuffle,
	})
	if err != nil {
		return fmt.Errorf("failed to fetch examples: %w", err)
	}
	examples := examplesResp.Examples
	if len(examples) == 0 {
		return fmt.Errorf("no examples found in dataset %s", run.DatasetID)
	}

	// Apply sample size if configured
	if run.Config.SampleSize > 0 && run.Config.SampleSize < len(examples) {
		examples = examples[:run.Config.SampleSize]
	}

	run.TotalExamples = len(examples)
	r.store.UpdateEvalRun(ctx, run)

	// Step 3: Process each example
	var results []*EvalResult
	var totalScore float64
	var passedCount int
	var totalLatency float64
	var totalTokens int
	var totalCost float64
	scoresByEvaluator := make(map[string]float64)
	countByEvaluator := make(map[string]int)

	for i, example := range examples {
		result, err := r.processExample(ctx, run, prompt, example)
		if err != nil {
			r.logger.Warn("failed to process example", "example_id", example.Id, "error", err)
			result = &EvalResult{
				ID:          uuid.New().String(),
				EvalRunID:   run.ID,
				ExampleID:   example.Id,
				Input:       example.Input.AsMap(),
				Error:       err.Error(),
				Passed:      false,
				OverallScore: 0,
			}
		}

		results = append(results, result)

		// Aggregate metrics
		totalScore += result.OverallScore
		if result.Passed {
			passedCount++
		}
		totalLatency += result.LatencyMs
		totalTokens += result.TokensUsed
		totalCost += result.CostUSD

		for evalType, evalResult := range result.EvaluatorResults {
			scoresByEvaluator[evalType] += evalResult.Score
			countByEvaluator[evalType]++
		}

		// Update progress
		run.CompletedExamples = i + 1
		r.store.UpdateEvalRun(ctx, run)
	}

	// Step 4: Store results
	for _, result := range results {
		if err := r.store.AddEvalResult(ctx, result); err != nil {
			r.logger.Warn("failed to store result", "result_id", result.ID, "error", err)
		}
	}

	// Step 5: Compute summary
	n := float64(len(results))
	avgScoresByEvaluator := make(map[string]float64)
	for evalType, total := range scoresByEvaluator {
		avgScoresByEvaluator[evalType] = total / float64(countByEvaluator[evalType])
	}

	run.Summary = &EvalSummary{
		OverallScore:      totalScore / n,
		ScoresByEvaluator: avgScoresByEvaluator,
		PassedCount:       passedCount,
		FailedCount:       len(results) - passedCount,
		PassRate:          float64(passedCount) / n,
		TotalCostUSD:      totalCost,
		TotalTokens:       totalTokens,
		AvgLatencyMs:      totalLatency / n,
	}

	return nil
}

func (r *Runner) processExample(ctx context.Context, run *EvalRun, prompt *promptv1.Prompt, example *datasetsv1.Example) (*EvalResult, error) {
	startTime := time.Now()

	// Build messages from prompt template
	messages := r.renderPrompt(prompt, example.Input.AsMap())

	// Call runtime for completion
	completeResp, err := r.runtimeClient.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages:    messages,
			Provider:    run.Config.Provider,
			Model:       run.Config.Model,
			Temperature: 0, // Deterministic for eval
			MaxTokens:   1000,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("completion failed: %w", err)
	}

	latencyMs := float64(time.Since(startTime).Milliseconds())

	// Extract actual output
	actualOutput := map[string]interface{}{
		"content": completeResp.Content,
	}
	if completeResp.Message != nil {
		actualOutput["role"] = completeResp.Message.Role
	}

	// Expected output from example
	expectedOutput := example.ExpectedOutput.AsMap()

	// Run evaluators
	evaluatorResults := make(map[string]*EvaluatorResult)
	var weightedScore float64
	var totalWeight float64

	for _, evalConfig := range run.Config.Evaluators {
		evalFn, ok := GetEvaluator(evalConfig.Type)
		if !ok {
			// Handle special evaluators (llm_judge, semantic_similarity)
			evalResult, err := r.runSpecialEvaluator(ctx, evalConfig, expectedOutput, actualOutput)
			if err != nil {
				r.logger.Warn("evaluator failed", "type", evalConfig.Type, "error", err)
				continue
			}
			evaluatorResults[evalConfig.Type] = evalResult
			weightedScore += evalResult.Score * evalConfig.Weight
			totalWeight += evalConfig.Weight
			continue
		}

		evalResult, err := evalFn(ctx, expectedOutput, actualOutput, evalConfig.Params)
		if err != nil {
			r.logger.Warn("evaluator failed", "type", evalConfig.Type, "error", err)
			continue
		}

		evaluatorResults[evalConfig.Type] = evalResult
		weightedScore += evalResult.Score * evalConfig.Weight
		totalWeight += evalConfig.Weight
	}

	// Calculate overall score
	overallScore := 0.0
	if totalWeight > 0 {
		overallScore = weightedScore / totalWeight
	}

	// Determine pass/fail (default threshold: 0.5)
	passed := overallScore >= 0.5

	// Extract usage info
	tokensUsed := 0
	costUSD := 0.0
	if completeResp.Usage != nil {
		tokensUsed = int(completeResp.Usage.TotalTokens)
		costUSD = completeResp.Usage.CostUsd
	}

	return &EvalResult{
		ID:               uuid.New().String(),
		EvalRunID:        run.ID,
		ExampleID:        example.Id,
		Input:            example.Input.AsMap(),
		ExpectedOutput:   expectedOutput,
		ActualOutput:     actualOutput,
		EvaluatorResults: evaluatorResults,
		OverallScore:     overallScore,
		Passed:           passed,
		LatencyMs:        latencyMs,
		TokensUsed:       tokensUsed,
		CostUSD:          costUSD,
	}, nil
}

func (r *Runner) renderPrompt(prompt *promptv1.Prompt, variables map[string]interface{}) []*runtimev1.Message {
	var messages []*runtimev1.Message

	for _, msg := range prompt.Messages {
		content := msg.Content

		// Replace variables in content
		for k, v := range variables {
			placeholder := fmt.Sprintf("{{%s}}", k)
			content = strings.ReplaceAll(content, placeholder, fmt.Sprintf("%v", v))
		}

		messages = append(messages, &runtimev1.Message{
			Role:    msg.Role,
			Content: content,
		})
	}

	return messages
}

func (r *Runner) runSpecialEvaluator(ctx context.Context, config EvaluatorConfig, expected, actual map[string]interface{}) (*EvaluatorResult, error) {
	switch config.Type {
	case "llm_judge":
		return r.evaluateLLMJudge(ctx, config, expected, actual)
	case "semantic_similarity":
		return r.evaluateSemanticSimilarity(ctx, config, expected, actual)
	default:
		return nil, fmt.Errorf("unknown evaluator type: %s", config.Type)
	}
}

func (r *Runner) evaluateLLMJudge(ctx context.Context, config EvaluatorConfig, expected, actual map[string]interface{}) (*EvaluatorResult, error) {
	criteria := config.Params["criteria"]
	if criteria == "" {
		criteria = "Is the response accurate, relevant, and well-written?"
	}

	model := config.Params["model"]
	if model == "" {
		model = "gpt-4o"
	}

	// Build judge prompt
	expectedStr := extractStringValue(expected)
	actualStr := extractStringValue(actual)

	judgePrompt := fmt.Sprintf(`You are evaluating an AI response. Score from 0 to 10.

Expected Output: %s

Actual Output: %s

Evaluation Criteria: %s

Respond with ONLY a JSON object in this format:
{"score": <0-10>, "explanation": "<brief explanation>"}`, expectedStr, actualStr, criteria)

	// Call runtime for judgment
	resp, err := r.runtimeClient.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: judgePrompt},
			},
			Model:       model,
			Temperature: 0,
			MaxTokens:   200,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("llm judge call failed: %w", err)
	}

	// Parse response
	var judgeResult struct {
		Score       float64 `json:"score"`
		Explanation string  `json:"explanation"`
	}

	content := resp.Content
	// Try to extract JSON from response
	if idx := strings.Index(content, "{"); idx >= 0 {
		content = content[idx:]
	}
	if idx := strings.LastIndex(content, "}"); idx >= 0 {
		content = content[:idx+1]
	}

	if err := json.Unmarshal([]byte(content), &judgeResult); err != nil {
		// Default to mid-score if parsing fails
		judgeResult.Score = 5
		judgeResult.Explanation = "Could not parse judge response: " + resp.Content
	}

	// Normalize score to 0-1
	normalizedScore := judgeResult.Score / 10.0
	if normalizedScore < 0 {
		normalizedScore = 0
	}
	if normalizedScore > 1 {
		normalizedScore = 1
	}

	return &EvaluatorResult{
		EvaluatorType: "llm_judge",
		Score:         normalizedScore,
		Passed:        normalizedScore >= 0.5,
		Explanation:   judgeResult.Explanation,
		Details: map[string]string{
			"criteria":  criteria,
			"raw_score": fmt.Sprintf("%.0f/10", judgeResult.Score),
		},
	}, nil
}

func (r *Runner) evaluateSemanticSimilarity(ctx context.Context, config EvaluatorConfig, expected, actual map[string]interface{}) (*EvaluatorResult, error) {
	threshold := 0.8
	if t, ok := config.Params["threshold"]; ok {
		fmt.Sscanf(t, "%f", &threshold)
	}

	model := config.Params["model"]
	if model == "" {
		model = "text-embedding-3-small"
	}

	expectedStr := extractStringValue(expected)
	actualStr := extractStringValue(actual)

	// Get embeddings for both texts
	expectedEmbed, err := r.runtimeClient.Embed(ctx, &runtimev1.EmbedRequest{
		Texts: []string{expectedStr},
		Model: model,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to embed expected: %w", err)
	}

	actualEmbed, err := r.runtimeClient.Embed(ctx, &runtimev1.EmbedRequest{
		Texts: []string{actualStr},
		Model: model,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to embed actual: %w", err)
	}

	if len(expectedEmbed.Embeddings) == 0 || len(actualEmbed.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}

	// Calculate cosine similarity
	similarity := cosineSimilarity(expectedEmbed.Embeddings[0].Values, actualEmbed.Embeddings[0].Values)

	return &EvaluatorResult{
		EvaluatorType: "semantic_similarity",
		Score:         similarity,
		Passed:        similarity >= threshold,
		Explanation:   fmt.Sprintf("Cosine similarity: %.3f (threshold: %.2f)", similarity, threshold),
		Details: map[string]string{
			"similarity": fmt.Sprintf("%.4f", similarity),
			"threshold":  fmt.Sprintf("%.2f", threshold),
			"model":      model,
		},
	}, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
