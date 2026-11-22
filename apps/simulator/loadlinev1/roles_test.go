package loadlinev1

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
)

// cloudflareQueueArchitecture: client → Workers API → Queues → Workers
// consumer. The consumer is declared COMPONENT_KIND_WORKER with a
// provider/service, which the converter used to rewrite into an API
// server (Workers' primary role) and then fail with "queue must link to
// exactly one worker".
func cloudflareQueueArchitecture(consumerService string) *v1.Architecture {
	return &v1.Architecture{
		SchemaVersion: "1",
		Name:          "cf-queue-consumer",
		Components: []*v1.ComponentSpec{
			{Id: "client", Kind: v1.ComponentKind_COMPONENT_KIND_CLIENT},
			{Id: "api", Kind: v1.ComponentKind_COMPONENT_KIND_API_SERVER,
				Provider: "cloudflare", Service: "workers"},
			{Id: "jobs", Kind: v1.ComponentKind_COMPONENT_KIND_QUEUE,
				Provider: "cloudflare", Service: "queues"},
			{Id: "consumer", Kind: v1.ComponentKind_COMPONENT_KIND_WORKER,
				Provider: "cloudflare", Service: consumerService,
				Concurrency: 32, DefaultServiceTimeMillis: 20},
		},
		Links: []*v1.Link{
			{From: "client", To: "api"},
			{From: "api", To: "jobs"},
			{From: "jobs", To: "consumer"},
		},
	}
}

func TestProviderBackedQueueConsumerRuns(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	created, err := c.CreateSimulation(ctx, connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: cloudflareQueueArchitecture("workers"),
		Workload:     testWorkload(),
		Options:      &v1.SimulationOptions{Seed: 7, DurationMs: 2_000},
	}))
	if err != nil {
		t.Fatalf("CreateSimulation: %v", err)
	}
	id := created.Msg.GetSimulation().GetId()
	if _, err := c.RunSimulation(ctx, connect.NewRequest(&v1.RunSimulationRequest{SimulationId: id})); err != nil {
		t.Fatalf("RunSimulation: %v", err)
	}
	st := waitForCompleted(t, c, id)
	if st.GetStatus() != v1.RunStatus_RUN_STATUS_COMPLETED {
		t.Fatalf("status = %s (%s); want COMPLETED", st.GetStatus(), st.GetError())
	}
}

func TestUnsupportedProviderRoleIsRejectedAtCreate(t *testing.T) {
	c := newTestClient(t)
	_, err := c.CreateSimulation(context.Background(), connect.NewRequest(&v1.CreateSimulationRequest{
		Architecture: cloudflareQueueArchitecture("durable_objects"),
		Workload:     testWorkload(),
	}))
	if err == nil {
		t.Fatal("durable_objects declared as a worker must be rejected")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "cannot run as worker") {
		t.Fatalf("want InvalidArgument naming the role, got %v", err)
	}
}
