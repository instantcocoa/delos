// Package integration contains comprehensive integration tests for all Delos services.
//
//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	datasetsv1 "github.com/instantcocoa/delos/gen/go/datasets/v1"
	deployv1 "github.com/instantcocoa/delos/gen/go/deploy/v1"
	evalv1 "github.com/instantcocoa/delos/gen/go/eval/v1"
	observev1 "github.com/instantcocoa/delos/gen/go/observe/v1"
	promptv1 "github.com/instantcocoa/delos/gen/go/prompt/v1"
	runtimev1 "github.com/instantcocoa/delos/gen/go/runtime/v1"
)

// ============================================================================
// OBSERVE SERVICE TESTS (5 endpoints)
// ============================================================================

func getObserveClient(t *testing.T) (observev1.ObserveServiceClient, func()) {
	t.Helper()
	addr := os.Getenv("DELOS_OBSERVE_ADDR")
	if addr == "" {
		addr = "localhost:9000"
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect to observe service: %v", err)
	}
	return observev1.NewObserveServiceClient(conn), func() { conn.Close() }
}

func TestObserveService_Health(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &observev1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Observe health: %s", resp.Status)
}

func TestObserveService_IngestTraces(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := timestamppb.Now()
	resp, err := client.IngestTraces(ctx, &observev1.IngestTracesRequest{
		Spans: []*observev1.Span{
			{
				TraceId:   "test-trace-123",
				SpanId:    "span-1",
				Name:      "test-operation",
				StartTime: now,
			},
		},
	})
	if err != nil {
		t.Fatalf("IngestTraces failed: %v", err)
	}
	t.Logf("Ingested %d spans", resp.AcceptedCount)
}

func TestObserveService_QueryTraces(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.QueryTraces(ctx, &observev1.QueryTracesRequest{
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryTraces failed: %v", err)
	}
	t.Logf("Found %d traces", len(resp.Traces))
}

func TestObserveService_GetTrace(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetTrace(ctx, &observev1.GetTraceRequest{
		TraceId: "test-trace-123",
	})
	if err != nil {
		t.Logf("GetTrace: %v (trace may not exist)", err)
		return
	}
	if resp.Trace != nil {
		t.Logf("Got trace: %s with %d spans", resp.Trace.TraceId, len(resp.Trace.Spans))
	}
}

func TestObserveService_QueryMetrics(t *testing.T) {
	client, cleanup := getObserveClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.QueryMetrics(ctx, &observev1.QueryMetricsRequest{
		MetricName: "request_count",
		StartTime:  timestamppb.New(time.Now().Add(-time.Hour)),
		EndTime:    timestamppb.Now(),
	})
	if err != nil {
		t.Fatalf("QueryMetrics failed: %v", err)
	}
	t.Logf("QueryMetrics returned successfully")
	_ = resp
}

// ============================================================================
// RUNTIME SERVICE TESTS (5 endpoints)
// ============================================================================

func getRuntimeClient(t *testing.T) (runtimev1.RuntimeServiceClient, func()) {
	t.Helper()
	addr := os.Getenv("DELOS_RUNTIME_ADDR")
	if addr == "" {
		addr = "localhost:9001"
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect to runtime service: %v", err)
	}
	return runtimev1.NewRuntimeServiceClient(conn), func() { conn.Close() }
}

func TestRuntimeService_Health(t *testing.T) {
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &runtimev1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Runtime health: %s, version: %s", resp.Status, resp.Version)
	for provider, available := range resp.ProviderStatus {
		t.Logf("  Provider %s: available=%v", provider, available)
	}
}

func TestRuntimeService_ListProviders(t *testing.T) {
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListProviders(ctx, &runtimev1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}
	t.Logf("Found %d providers:", len(resp.Providers))
	for _, p := range resp.Providers {
		t.Logf("  %s: available=%v, models=%d", p.Name, p.Available, len(p.Models))
	}
}

func TestRuntimeService_Complete(t *testing.T) {
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, &runtimev1.CompleteRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "Say hello"},
			},
			MaxTokens: 10,
		},
	})
	if err != nil {
		t.Logf("Complete: %v (expected if no API keys)", err)
		return
	}
	t.Logf("Complete response: %s", resp.Content)
}

func TestRuntimeService_Embed(t *testing.T) {
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Embed(ctx, &runtimev1.EmbedRequest{
		Texts: []string{"hello world"},
	})
	if err != nil {
		t.Logf("Embed: %v (expected if no API keys)", err)
		return
	}
	t.Logf("Embed: got %d embeddings", len(resp.Embeddings))
}

func TestRuntimeService_CompleteStream(t *testing.T) {
	client, cleanup := getRuntimeClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stream, err := client.CompleteStream(ctx, &runtimev1.CompleteStreamRequest{
		Params: &runtimev1.CompletionParams{
			Messages: []*runtimev1.Message{
				{Role: "user", Content: "Say hello"},
			},
		},
	})
	if err != nil {
		t.Logf("CompleteStream: %v (expected if no API keys)", err)
		return
	}

	chunks := 0
	for {
		_, err := stream.Recv()
		if err != nil {
			if err.Error() != "EOF" {
				t.Logf("CompleteStream recv: %v (expected if no API keys)", err)
			}
			break
		}
		chunks++
	}
	t.Logf("CompleteStream: received %d chunks", chunks)
}

// ============================================================================
// PROMPT SERVICE TESTS (8 endpoints) - Health test
// ============================================================================

func TestPromptService_Health(t *testing.T) {
	client, cleanup := getPromptClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &promptv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Prompt health: %s", resp.Status)
}

// ============================================================================
// DATASETS SERVICE TESTS (10 endpoints)
// ============================================================================

func TestDatasetsService_Health(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &datasetsv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Datasets health: %s", resp.Status)
}

func TestDatasetsService_FullCRUD(t *testing.T) {
	client, cleanup := getDatasetsClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. CreateDataset
	createResp, err := client.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:        "CRUD Test Dataset",
		Description: "Testing all CRUD operations",
		Tags:        []string{"crud-test"},
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}
	datasetID := createResp.Dataset.Id
	t.Logf("1. Created dataset: %s", datasetID)

	defer func() {
		client.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})
	}()

	// 2. GetDataset
	getResp, err := client.GetDataset(ctx, &datasetsv1.GetDatasetRequest{Id: datasetID})
	if err != nil {
		t.Fatalf("GetDataset failed: %v", err)
	}
	t.Logf("2. Got dataset: %s", getResp.Dataset.Name)

	// 3. UpdateDataset
	updateResp, err := client.UpdateDataset(ctx, &datasetsv1.UpdateDatasetRequest{
		Id:          datasetID,
		Description: "Updated description",
	})
	if err != nil {
		t.Fatalf("UpdateDataset failed: %v", err)
	}
	t.Logf("3. Updated dataset: %s", updateResp.Dataset.Description)

	// 4. ListDatasets
	listResp, err := client.ListDatasets(ctx, &datasetsv1.ListDatasetsRequest{
		Tags:  []string{"crud-test"},
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListDatasets failed: %v", err)
	}
	t.Logf("4. Listed %d datasets", len(listResp.Datasets))

	// 5. AddExamples
	addResp, err := client.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: datasetID,
		Examples: []*datasetsv1.ExampleInput{
			{
				Input:          toStruct(t, map[string]interface{}{"q": "What is 2+2?"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"a": "4"}),
			},
			{
				Input:          toStruct(t, map[string]interface{}{"q": "What is 3+3?"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"a": "6"}),
			},
		},
	})
	if err != nil {
		t.Fatalf("AddExamples failed: %v", err)
	}
	t.Logf("5. Added %d examples", addResp.AddedCount)

	// 6. GetExamples
	getExResp, err := client.GetExamples(ctx, &datasetsv1.GetExamplesRequest{
		DatasetId: datasetID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetExamples failed: %v", err)
	}
	t.Logf("6. Got %d examples (total: %d)", len(getExResp.Examples), getExResp.TotalCount)

	// 7. RemoveExamples
	if len(getExResp.Examples) > 0 {
		removeResp, err := client.RemoveExamples(ctx, &datasetsv1.RemoveExamplesRequest{
			DatasetId:  datasetID,
			ExampleIds: []string{getExResp.Examples[0].Id},
		})
		if err != nil {
			t.Fatalf("RemoveExamples failed: %v", err)
		}
		t.Logf("7. Removed %d examples", removeResp.RemovedCount)
	}

	// 8. GenerateExamples (may require LLM)
	genResp, err := client.GenerateExamples(ctx, &datasetsv1.GenerateExamplesRequest{
		DatasetId: datasetID,
		Count:     2,
	})
	if err != nil {
		t.Logf("8. GenerateExamples: %v (may require LLM)", err)
	} else {
		t.Logf("8. Generated %d examples", genResp.GeneratedCount)
	}

	// 9. DeleteDataset (in defer)
	t.Logf("9. DeleteDataset will run in defer")
}

// ============================================================================
// EVAL SERVICE TESTS (8 endpoints)
// ============================================================================

func getEvalClient(t *testing.T) (evalv1.EvalServiceClient, func()) {
	t.Helper()
	addr := os.Getenv("DELOS_EVAL_ADDR")
	if addr == "" {
		addr = "localhost:9004"
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect to eval service: %v", err)
	}
	return evalv1.NewEvalServiceClient(conn), func() { conn.Close() }
}

func TestEvalService_Health(t *testing.T) {
	client, cleanup := getEvalClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &evalv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Eval health: %s", resp.Status)
}

func TestEvalService_ListEvaluators(t *testing.T) {
	client, cleanup := getEvalClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListEvaluators(ctx, &evalv1.ListEvaluatorsRequest{})
	if err != nil {
		t.Fatalf("ListEvaluators failed: %v", err)
	}
	t.Logf("Found %d evaluators:", len(resp.Evaluators))
	for _, e := range resp.Evaluators {
		t.Logf("  %s: %s", e.Type, e.Name)
	}
}

func TestEvalService_FullWorkflow(t *testing.T) {
	evalClient, evalCleanup := getEvalClient(t)
	defer evalCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	datasetsClient, datasetsCleanup := getDatasetsClient(t)
	defer datasetsCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create a prompt
	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Eval Test Prompt",
		Slug: "eval-test-" + time.Now().Format("150405"),
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Echo back the input"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	promptID := promptResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	t.Logf("Created prompt: %s", promptID)

	// Create a dataset
	datasetResp, err := datasetsClient.CreateDataset(ctx, &datasetsv1.CreateDatasetRequest{
		Name:     "Eval Test Dataset",
		PromptId: promptID,
	})
	if err != nil {
		t.Fatalf("CreateDataset failed: %v", err)
	}
	datasetID := datasetResp.Dataset.Id
	defer datasetsClient.DeleteDataset(ctx, &datasetsv1.DeleteDatasetRequest{Id: datasetID})
	t.Logf("Created dataset: %s", datasetID)

	// Add examples
	_, err = datasetsClient.AddExamples(ctx, &datasetsv1.AddExamplesRequest{
		DatasetId: datasetID,
		Examples: []*datasetsv1.ExampleInput{
			{
				Input:          toStruct(t, map[string]interface{}{"text": "hello"}),
				ExpectedOutput: toStruct(t, map[string]interface{}{"text": "hello"}),
			},
		},
	})
	if err != nil {
		t.Fatalf("AddExamples failed: %v", err)
	}

	// 1. CreateEvalRun
	createRunResp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "Test Eval Run",
		PromptId:      promptID,
		PromptVersion: 1,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "exact_match", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateEvalRun failed: %v", err)
	}
	runID := createRunResp.EvalRun.Id
	t.Logf("1. Created eval run: %s", runID)

	// 2. GetEvalRun
	getRunResp, err := evalClient.GetEvalRun(ctx, &evalv1.GetEvalRunRequest{Id: runID})
	if err != nil {
		t.Fatalf("GetEvalRun failed: %v", err)
	}
	t.Logf("2. Got eval run: status=%s", getRunResp.EvalRun.Status)

	// 3. ListEvalRuns
	listRunsResp, err := evalClient.ListEvalRuns(ctx, &evalv1.ListEvalRunsRequest{
		PromptId: promptID,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListEvalRuns failed: %v", err)
	}
	t.Logf("3. Listed %d eval runs", len(listRunsResp.EvalRuns))

	// 4. GetEvalResults
	resultsResp, err := evalClient.GetEvalResults(ctx, &evalv1.GetEvalResultsRequest{
		EvalRunId: runID,
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("GetEvalResults failed: %v", err)
	}
	t.Logf("4. Got %d results", len(resultsResp.Results))

	// 5. CancelEvalRun
	_, err = evalClient.CancelEvalRun(ctx, &evalv1.CancelEvalRunRequest{Id: runID})
	if err != nil {
		t.Logf("5. CancelEvalRun: %v (may already be complete)", err)
	} else {
		t.Logf("5. Cancelled eval run")
	}

	// 6. CompareRuns - create another run first
	createRun2Resp, err := evalClient.CreateEvalRun(ctx, &evalv1.CreateEvalRunRequest{
		Name:          "Test Eval Run 2",
		PromptId:      promptID,
		PromptVersion: 1,
		DatasetId:     datasetID,
		Config: &evalv1.EvalConfig{
			Evaluators: []*evalv1.EvaluatorConfig{
				{Type: "exact_match", Weight: 1.0},
			},
		},
	})
	if err != nil {
		t.Logf("6. CreateEvalRun 2: %v", err)
	} else {
		compareResp, err := evalClient.CompareRuns(ctx, &evalv1.CompareRunsRequest{
			RunIdA: runID,
			RunIdB: createRun2Resp.EvalRun.Id,
		})
		if err != nil {
			t.Logf("6. CompareRuns: %v", err)
		} else {
			t.Logf("6. Compared runs: score_diff=%f, regressions=%d, improvements=%d", compareResp.ScoreDiff, compareResp.Regressions, compareResp.Improvements)
		}
	}
}

// ============================================================================
// DEPLOY SERVICE TESTS (10 endpoints)
// ============================================================================

func getDeployClient(t *testing.T) (deployv1.DeployServiceClient, func()) {
	t.Helper()
	addr := os.Getenv("DELOS_DEPLOY_ADDR")
	if addr == "" {
		addr = "localhost:9005"
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to connect to deploy service: %v", err)
	}
	return deployv1.NewDeployServiceClient(conn), func() { conn.Close() }
}

func TestDeployService_Health(t *testing.T) {
	client, cleanup := getDeployClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.Health(ctx, &deployv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	t.Logf("Deploy health: %s", resp.Status)
}

func TestDeployService_FullWorkflow(t *testing.T) {
	deployClient, deployCleanup := getDeployClient(t)
	defer deployCleanup()

	promptClient, promptCleanup := getPromptClient(t)
	defer promptCleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create a prompt to deploy
	promptResp, err := promptClient.CreatePrompt(ctx, &promptv1.CreatePromptRequest{
		Name: "Deploy Test Prompt",
		Slug: "deploy-test-" + time.Now().Format("150405"),
		Messages: []*promptv1.PromptMessage{
			{Role: "system", Content: "Test prompt"},
		},
	})
	if err != nil {
		t.Fatalf("CreatePrompt failed: %v", err)
	}
	promptID := promptResp.Prompt.Id
	defer promptClient.DeletePrompt(ctx, &promptv1.DeletePromptRequest{Id: promptID})
	t.Logf("Created prompt: %s", promptID)

	// 1. CreateDeployment
	createDeployResp, err := deployClient.CreateDeployment(ctx, &deployv1.CreateDeploymentRequest{
		PromptId:    promptID,
		ToVersion:   1,
		Environment: "staging",
		Strategy: &deployv1.DeploymentStrategy{
			Type: deployv1.DeploymentType_DEPLOYMENT_TYPE_IMMEDIATE,
		},
	})
	if err != nil {
		t.Fatalf("CreateDeployment failed: %v", err)
	}
	deploymentID := createDeployResp.Deployment.Id
	t.Logf("1. Created deployment: %s", deploymentID)

	// 2. GetDeployment
	getDeployResp, err := deployClient.GetDeployment(ctx, &deployv1.GetDeploymentRequest{
		Id: deploymentID,
	})
	if err != nil {
		t.Fatalf("GetDeployment failed: %v", err)
	}
	t.Logf("2. Got deployment: status=%s", getDeployResp.Deployment.Status)

	// 3. ListDeployments
	listDeployResp, err := deployClient.ListDeployments(ctx, &deployv1.ListDeploymentsRequest{
		PromptId: promptID,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListDeployments failed: %v", err)
	}
	t.Logf("3. Listed %d deployments", len(listDeployResp.Deployments))

	// 4. GetDeploymentStatus
	statusResp, err := deployClient.GetDeploymentStatus(ctx, &deployv1.GetDeploymentStatusRequest{
		Id: deploymentID,
	})
	if err != nil {
		t.Fatalf("GetDeploymentStatus failed: %v", err)
	}
	t.Logf("4. Deployment status: %s", statusResp.Status)

	// 5. ApproveDeployment
	approveResp, err := deployClient.ApproveDeployment(ctx, &deployv1.ApproveDeploymentRequest{
		Id: deploymentID,
	})
	if err != nil {
		t.Logf("5. ApproveDeployment: %v (may already be approved)", err)
	} else {
		t.Logf("5. Approved deployment: %s", approveResp.Deployment.Status)
	}

	// 6. CreateQualityGate
	gateResp, err := deployClient.CreateQualityGate(ctx, &deployv1.CreateQualityGateRequest{
		Name:     "min-score-gate",
		PromptId: promptID,
		Conditions: []*deployv1.GateCondition{
			{
				Type:      "eval_score",
				Operator:  "gte",
				Threshold: 0.8,
			},
		},
		Required: true,
	})
	if err != nil {
		t.Logf("6. CreateQualityGate: %v", err)
	} else {
		t.Logf("6. Created quality gate: %s", gateResp.QualityGate.Id)
	}

	// 7. ListQualityGates
	listGatesResp, err := deployClient.ListQualityGates(ctx, &deployv1.ListQualityGatesRequest{
		PromptId: promptID,
	})
	if err != nil {
		t.Fatalf("ListQualityGates failed: %v", err)
	}
	t.Logf("7. Listed %d quality gates", len(listGatesResp.QualityGates))

	// 8. Create second deployment for rollback test
	createDeploy2Resp, err := deployClient.CreateDeployment(ctx, &deployv1.CreateDeploymentRequest{
		PromptId:    promptID,
		ToVersion:   1,
		Environment: "staging",
		Strategy: &deployv1.DeploymentStrategy{
			Type: deployv1.DeploymentType_DEPLOYMENT_TYPE_IMMEDIATE,
		},
	})
	if err != nil {
		t.Logf("8. CreateDeployment 2: %v", err)
	} else {
		// 9. RollbackDeployment
		rollbackResp, err := deployClient.RollbackDeployment(ctx, &deployv1.RollbackDeploymentRequest{
			Id:     createDeploy2Resp.Deployment.Id,
			Reason: "Testing rollback",
		})
		if err != nil {
			t.Logf("9. RollbackDeployment: %v", err)
		} else {
			t.Logf("9. Rolled back deployment: %s", rollbackResp.Deployment.Status)
		}
	}

	// 10. CancelDeployment
	cancelResp, err := deployClient.CancelDeployment(ctx, &deployv1.CancelDeploymentRequest{
		Id:     deploymentID,
		Reason: "Testing cancellation",
	})
	if err != nil {
		t.Logf("10. CancelDeployment: %v", err)
	} else {
		t.Logf("10. Cancelled deployment: %s", cancelResp.Deployment.Status)
	}
}
