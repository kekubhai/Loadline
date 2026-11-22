package providers

import (
	"strings"
	"testing"

	"github.com/kekubhai/Loadline/apps/simulator/sim"
)

func TestResolveKindDefaultsToPrimaryRole(t *testing.T) {
	k, err := ResolveKind("cloudflare", "workers", "")
	if err != nil || k != sim.KindAPIServer {
		t.Fatalf("got %q, %v; want api_server", k, err)
	}
	k, err = ResolveKind("cloudflare", "workers", sim.KindAPIServer)
	if err != nil || k != sim.KindAPIServer {
		t.Fatalf("got %q, %v; want api_server", k, err)
	}
}

func TestResolveKindHonorsQueueConsumerRole(t *testing.T) {
	for _, key := range [][2]string{
		{"cloudflare", "workers"}, {"aws", "lambda"}, {"aws", "ec2"}, {"gcp", "cloud_run"},
	} {
		k, err := ResolveKind(key[0], key[1], sim.KindWorker)
		if err != nil || k != sim.KindWorker {
			t.Errorf("%s/%s as worker: got %q, %v", key[0], key[1], k, err)
		}
	}
}

func TestResolveKindRejectsUnsupportedRole(t *testing.T) {
	_, err := ResolveKind("cloudflare", "durable_objects", sim.KindWorker)
	if err == nil {
		t.Fatal("durable_objects must not run as a queue consumer")
	}
	if !strings.Contains(err.Error(), "cannot run as worker") {
		t.Fatalf("unhelpful error: %v", err)
	}
	if _, err := ResolveKind("aws", "rds", sim.KindCache); err == nil {
		t.Fatal("rds must not run as a cache")
	}
}

func TestBuildSpecUsesRequestedRole(t *testing.T) {
	rs, err := ResolveService("consumer", "cloudflare", "workers", Config{Kind: sim.KindWorker, Concurrency: 64})
	if err != nil {
		t.Fatalf("ResolveService: %v", err)
	}
	if rs.Spec.Kind != sim.KindWorker || rs.Spec.Concurrency != 64 {
		t.Fatalf("spec = %+v; want worker with concurrency 64", rs.Spec)
	}
	found := false
	for _, a := range rs.Assumptions {
		if strings.Contains(a, "runs as worker") {
			found = true
		}
	}
	if !found {
		t.Fatalf("role change must be recorded as an assumption: %v", rs.Assumptions)
	}
}

// A queue drained by a provider-backed consumer must validate and run.
func TestCloudflareQueueWithWorkersConsumerRuns(t *testing.T) {
	api, err := ResolveService("api", "cloudflare", "workers", Config{})
	if err != nil {
		t.Fatal(err)
	}
	q, err := ResolveService("jobs", "cloudflare", "queues", Config{})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := ResolveService("consumer", "cloudflare", "workers", Config{Kind: sim.KindWorker, Concurrency: 32})
	if err != nil {
		t.Fatal(err)
	}
	arch := sim.Architecture{
		Name: "cf-queue-consumer",
		Components: []sim.ComponentSpec{
			{ID: "client", Kind: sim.KindClient},
			api.Spec, q.Spec, consumer.Spec,
		},
		Links: []sim.Link{{From: "client", To: "api"}, {From: "api", To: "jobs"}, {From: "jobs", To: "consumer"}},
	}
	if err := arch.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
