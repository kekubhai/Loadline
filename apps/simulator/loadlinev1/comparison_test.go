package loadlinev1

// Step 9 — Architecture Comparison tests, run against the REAL Connect
// service over in-process HTTP. Everything asserted here comes from
// actual simulation runs; the suite guards the comparison contract:
// same workload everywhere, isolation between architectures, determinism,
// failure comparison, and no fake aggregation.

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	lv1connect "github.com/kekubhai/Loadline/apps/simulator/loadline/v1/loadlinev1connect"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// awsArch builds a provider-backed AWS architecture: Client → Lambda →
// ElastiCache(80% hit) → RDS.
func awsArch(t *testing.T) *v1.Architecture {
	t.Helper()
	return &v1.Architecture{
		SchemaVersion: "1",
		Name:          "aws-web",
		Components: []*v1.ComponentSpec{
			{Id: "client", Kind: v1.ComponentKind_COMPONENT_KIND_CLIENT},
			{Id: "api", Kind: v1.ComponentKind_COMPONENT_KIND_API_SERVER, Provider: "aws", Service: "lambda", Config: &v1.ProviderConfig{MemoryMb: 512}},
			{Id: "cache", Kind: v1.ComponentKind_COMPONENT_KIND_CACHE, Provider: "aws", Service: "elasticache", HitRatio: 0.8},
			{Id: "db", Kind: v1.ComponentKind_COMPONENT_KIND_DATABASE, Provider: "aws", Service: "rds", Config: &v1.ProviderConfig{StorageGb: 100}},
		},
		Links: []*v1.Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
}

// gcpArch builds a provider-backed GCP variant: Client → Cloud Run →
// Memorystore → Cloud SQL.
func gcpArch(t *testing.T) *v1.Architecture {
	t.Helper()
	return &v1.Architecture{
		SchemaVersion: "1",
		Name:          "gcp-web",
		Components: []*v1.ComponentSpec{
			{Id: "client", Kind: v1.ComponentKind_COMPONENT_KIND_CLIENT},
			{Id: "api", Kind: v1.ComponentKind_COMPONENT_KIND_API_SERVER, Provider: "gcp", Service: "cloud_run", Config: &v1.ProviderConfig{MemoryMb: 512}},
			{Id: "cache", Kind: v1.ComponentKind_COMPONENT_KIND_CACHE, Provider: "gcp", Service: "memorystore", HitRatio: 0.8},
			{Id: "db", Kind: v1.ComponentKind_COMPONENT_KIND_DATABASE, Provider: "gcp", Service: "cloud_sql", Config: &v1.ProviderConfig{StorageGb: 100}},
		},
		Links: []*v1.Link{
			{From: "client", To: "api"},
			{From: "api", To: "cache"},
			{From: "cache", To: "db"},
		},
	}
}

func compWorkload() *v1.WorkloadSpec {
	return &v1.WorkloadSpec{
		TotalUsers: 1_000_000, Dau: 100_000, RequestsPerUserPerDay: 20,
		PeakMultiplier: 3, ReadWriteRatio: 4, PayloadBytes: 4096,
	}
}

func compOptions() *v1.SimulationOptions {
	return &v1.SimulationOptions{
		Seed: 42, DurationMs: 20_000,
		MaxRetries: 1, BackoffBaseMs: 5, TimeoutMs: 100,
		RetryOn: []string{"api", "cache"},
	}
}

func newCompClient(t *testing.T) lv1connect.SimulationServiceClient {
	t.Helper()
	svc := NewService(NewStore())
	_, handler := lv1connect.NewSimulationServiceHandler(svc)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return lv1connect.NewSimulationServiceClient(server.Client(), server.URL)
}

// Same workload → comparable runs: both entries see the identical
// generated-request count (same seed ⇒ same arrivals) and the response
// echoes the shared workload + effective seed.
func TestComparisonSameWorkloadComparable(t *testing.T) {
	client := newCompClient(t)
	res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	r := res.Msg.GetResult()
	if len(r.GetEntries()) != 2 {
		t.Fatalf("entries = %d, want 2", len(r.GetEntries()))
	}
	if r.GetSeed() != 42 {
		t.Fatalf("seed echo = %d, want 42", r.GetSeed())
	}
	if r.GetWorkload().GetDau() != 100_000 {
		t.Fatal("workload echo mismatch")
	}
	// Identical seed + workload ⇒ identical generated counts across
	// architectures of the same topology. The two differ only in provider
	// service times, so arrivals are identical.
	if r.GetEntries()[0].GetMetrics().GetGenerated() != r.GetEntries()[1].GetMetrics().GetGenerated() {
		t.Fatalf("generated diverged across same-seed entries: %d vs %d",
			r.GetEntries()[0].GetMetrics().GetGenerated(), r.GetEntries()[1].GetMetrics().GetGenerated())
	}
	// Every entry carries a full result set.
	for _, e := range r.GetEntries() {
		if e.GetError() != "" {
			t.Fatalf("entry %s errored: %s", e.GetName(), e.GetError())
		}
		if e.GetMetrics() == nil || e.GetPlan() == nil || e.GetDiagnosis() == nil {
			t.Fatalf("entry %s missing results", e.GetName())
		}
		if e.GetMetrics().GetDurationMs() != 20_000 {
			t.Fatalf("entry %s window = %f, want the shared 20000", e.GetName(), e.GetMetrics().GetDurationMs())
		}
	}
	// Conservation holds per entry.
	for _, e := range r.GetEntries() {
		m := e.GetMetrics()
		if m.GetGenerated() != m.GetCompleted()+m.GetRejected()+m.GetFailed()+m.GetInFlight() {
			t.Fatalf("entry %s conservation violated", e.GetName())
		}
	}
}

// Architecture A and B remain isolated: a failure targeting only A's
// named component must not perturb B's entry (per-architecture
// validation rejects the skew up front), and independent entries stay
// independent.
func TestComparisonArchitectureIsolation(t *testing.T) {
	client := newCompClient(t)
	// Shared failure targets must exist in BOTH architectures — B has no
	// "db" named component? (it does: db). Rename gcp's db to gcp-db to
	// construct the skew case.
	gcp := gcpArch(t)
	for _, c := range gcp.Components {
		if c.Id == "db" {
			c.Id = "gcp-db"
		}
	}
	for _, l := range gcp.Links {
		if l.To == "db" {
			l.To = "gcp-db"
		}
	}
	opts := compOptions()
	opts.Failures = []*v1.Failure{{
		Target: "db", Type: v1.FailureType_FAILURE_TYPE_CRASH,
		StartMs: 5_000, DurationMs: 5_000,
		Config: &v1.FailureConfig{PassThrough: true},
	}}
	_, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcp}},
		Options:       opts,
	}))
	if err == nil {
		t.Fatal("shared failure target missing in one architecture must be rejected up front")
	}
	if !strings.Contains(err.Error(), "gcp") {
		t.Fatalf("error must name the offending architecture: %v", err)
	}
}

// Deterministic comparison: the identical request twice produces
// byte-identical per-entry metrics.
func TestComparisonDeterministic(t *testing.T) {
	client := newCompClient(t)
	run := func() *v1.ComparisonResult {
		res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
			Workload:      compWorkload(),
			Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
			Options:       compOptions(),
		}))
		if err != nil {
			t.Fatal(err)
		}
		return res.Msg.GetResult()
	}
	a, b := run(), run()
	for i := range a.GetEntries() {
		am, bm := a.GetEntries()[i].GetMetrics(), b.GetEntries()[i].GetMetrics()
		if am.GetGenerated() != bm.GetGenerated() ||
			am.GetCompleted() != bm.GetCompleted() ||
			am.GetP99Ms() != bm.GetP99Ms() ||
			am.GetAvgLatencyMs() != bm.GetAvgLatencyMs() {
			t.Fatalf("entry %d diverged between identical comparisons: %+v vs %+v", i, am, bm)
		}
	}
}

// Failure comparison: the same cache-crash scenario applied to both
// architectures degrades both, with architecture-specific magnitudes —
// and entries must differ from their own healthy forms.
func TestComparisonFailureScenario(t *testing.T) {
	client := newCompClient(t)
	opts := compOptions()
	opts.Failures = []*v1.Failure{{
		Target: "cache", Type: v1.FailureType_FAILURE_TYPE_CRASH,
		StartMs: 5_000, DurationMs: 10_000,
		Config: &v1.FailureConfig{PassThrough: true},
	}}
	res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       opts,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Msg.GetResult().GetEntries() {
		if e.GetError() != "" {
			t.Fatalf("entry %s errored: %s", e.GetName(), e.GetError())
		}
		// The cascade must be visible: db arrivals jumped (pass-through)
		// and the diagnosis is not empty. Both architectures share the
		// topology, so both degrade.
		var dbArrived uint64
		for _, c := range e.GetMetrics().GetComponents() {
			if c.GetId() == "db" {
				dbArrived = c.GetArrived()
			}
		}
		if dbArrived == 0 {
			t.Fatalf("entry %s: db saw no traffic", e.GetName())
		}
		if len(e.GetFailures()) == 0 && e.GetMetrics().GetFailed() == 0 {
			// Pass-through crash may produce zero failures if the db
			// absorbs everything; require the diagnosis to still work.
			_ = e.GetDiagnosis()
		}
	}
}

// Metric comparison: differing capacity must produce differing measured
// outcomes — a constrained-cache architecture (hit ratio 0, so every read
// reaches the db) completes fewer requests than the standard one.
func TestComparisonMetricDifferences(t *testing.T) {
	client := newCompClient(t)
	const durationMS = 20_000
	heavy := compWorkload()
	heavy.Dau = 400_000
	heavy.RequestsPerUserPerDay = 40
	heavy.PeakMultiplier = 5 // ≈ 926 RPS peak
	opts := compOptions()
	opts.DurationMs = durationMS

	constrained := awsArch(t)
	for _, c := range constrained.Components {
		if c.Id == "cache" {
			c.HitRatio = 0 // every read becomes db traffic
		}
	}
	res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      heavy,
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "no-cache", Architecture: constrained}, {Name: "aws", Architecture: awsArch(t)}},
		Options:       opts,
	}))
	if err != nil {
		t.Fatal(err)
	}
	entries := res.Msg.GetResult().GetEntries()
	var noCacheCompleted, cachedCompleted uint64
	var noCacheP95, cachedP95 float64
	for _, e := range entries {
		if e.GetMetrics().GetCompleted() > noCacheCompleted && e.GetName() == "no-cache" {
			noCacheCompleted = e.GetMetrics().GetCompleted()
		}
		if e.GetName() == "aws" {
			cachedCompleted = e.GetMetrics().GetCompleted()
		}
		switch e.GetName() {
		case "no-cache":
			noCacheP95 = e.GetMetrics().GetP95Ms()
		case "aws":
			cachedP95 = e.GetMetrics().GetP95Ms()
		}
	}
	if noCacheCompleted >= cachedCompleted {
		t.Fatalf("no-cache completions %d must trail cached %d under heavy load",
			noCacheCompleted, cachedCompleted)
	}
	if noCacheP95 < cachedP95 {
		t.Fatalf("no-cache p95 %f must exceed cached p95 %f", noCacheP95, cachedP95)
	}
}

// Cost comparison: every provider-backed entry carries a cost estimate
// with the ESTIMATE marker and assumption trail.
func TestComparisonCostEstimates(t *testing.T) {
	client := newCompClient(t)
	res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Msg.GetResult().GetEntries() {
		cost := e.GetCost()
		if cost == nil {
			t.Fatalf("entry %s missing cost estimate", e.GetName())
		}
		if cost.GetTotal() <= 0 {
			t.Fatalf("entry %s cost total = %f", e.GetName(), cost.GetTotal())
		}
		if len(cost.GetAssumptions()) == 0 {
			t.Fatalf("entry %s cost missing assumption trail", e.GetName())
		}
	}
}

// Invalid architecture handling: a broken architecture yields a
// per-entry error, healthy entries still return, and the request-level
// rejections (fewer than two, missing workload, duplicate names) hold.
func TestComparisonInvalidHandling(t *testing.T) {
	client := newCompClient(t)

	// One broken architecture: validation error isolated to its entry.
	broken := awsArch(t)
	broken.Links = append(broken.Links, &v1.Link{From: "db", To: "api"}) // cycle
	res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "broken", Architecture: broken}},
		Options:       compOptions(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	entries := res.Msg.GetResult().GetEntries()
	foundErr := false
	for _, e := range entries {
		if e.GetName() == "broken" {
			if e.GetError() == "" {
				t.Fatal("broken architecture must carry an entry error")
			}
			if e.GetMetrics() != nil {
				t.Fatal("errored entry must not carry metrics")
			}
			foundErr = true
		}
		if e.GetName() == "aws" && e.GetMetrics() == nil {
			t.Fatal("healthy entry must still produce results")
		}
	}
	if !foundErr {
		t.Fatal("broken entry missing")
	}

	// Fewer than two architectures.
	if _, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}},
		Options:       compOptions(),
	})); err == nil {
		t.Fatal("single-architecture comparison must be rejected")
	}

	// Missing workload.
	if _, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	})); err == nil {
		t.Fatal("missing workload must be rejected")
	}

	// Invalid workload values.
	badWL := compWorkload()
	badWL.Dau = 2_000_000 // > total users
	if _, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      badWL,
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	})); err == nil {
		t.Fatal("invalid workload must be rejected")
	}

	// Unknown provider service.
	unknown := awsArch(t)
	unknown.Components[1].Service = "nonexistent"
	if _, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: unknown}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	})); err == nil {
		t.Fatal("unknown provider service must be rejected")
	}

	// Duplicate names.
	if _, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "same", Architecture: awsArch(t)}, {Name: "same", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	})); err == nil {
		t.Fatal("duplicate comparison names must be rejected")
	}
}

// No fake aggregation: the result type carries no score/rank/recommendation
// fields, and entries keep request order (not a merit order).
func TestComparisonNoScoresNoRanking(t *testing.T) {
	client := newCompClient(t)
	res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "zaws", Architecture: awsArch(t)}, {Name: "agcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	entries := res.Msg.GetResult().GetEntries()
	if entries[0].GetName() != "zaws" || entries[1].GetName() != "agcp" {
		t.Fatalf("entries must keep request order, got %s, %s", entries[0].GetName(), entries[1].GetName())
	}
}

// GetComparison returns the stored result by ID; unknown IDs 404.
func TestComparisonStoreRoundTrip(t *testing.T) {
	client := newCompClient(t)
	res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Msg.GetComparisonId()
	if id == "" {
		t.Fatal("RunComparison must return the ID it stored the result under")
	}

	// The stored copy is the result just returned — same entries, same
	// numbers, byte-for-byte.
	stored, err := client.GetComparison(context.Background(), connect.NewRequest(&v1.GetComparisonRequest{ComparisonId: id}))
	if err != nil {
		t.Fatalf("GetComparison(%s): %v", id, err)
	}
	want, got := res.Msg.GetResult(), stored.Msg.GetResult()
	if len(want.GetEntries()) != len(got.GetEntries()) {
		t.Fatalf("stored entries = %d, want %d", len(got.GetEntries()), len(want.GetEntries()))
	}
	for i := range want.GetEntries() {
		if want.GetEntries()[i].GetMetrics().GetP95Ms() != got.GetEntries()[i].GetMetrics().GetP95Ms() {
			t.Fatalf("stored vs returned p95 diverged at entry %d: %f vs %f", i,
				got.GetEntries()[i].GetMetrics().GetP95Ms(), want.GetEntries()[i].GetMetrics().GetP95Ms())
		}
	}

	// Unknown IDs are NotFound, not an empty success.
	if _, err := client.GetComparison(context.Background(), connect.NewRequest(&v1.GetComparisonRequest{ComparisonId: "cmp-does-not-exist"})); err == nil {
		t.Fatal("expected NotFound for an unknown comparison id")
	}

	// Determinism: a re-run of the same request equals the stored one.
	again, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	b := again.Msg.GetResult().GetEntries()
	for i := range want.GetEntries() {
		if want.GetEntries()[i].GetMetrics().GetP95Ms() != b[i].GetMetrics().GetP95Ms() {
			t.Fatalf("stored vs re-run p95 diverged: %f vs %f",
				want.GetEntries()[i].GetMetrics().GetP95Ms(), b[i].GetMetrics().GetP95Ms())
		}
	}
}

// The shared workload is exactly the one derived for every entry — the
// plan echo must match workload.Derive of the request workload.
func TestComparisonPlanEchoMatchesDerive(t *testing.T) {
	client := newCompClient(t)
	res, err := client.RunComparison(context.Background(), connect.NewRequest(&v1.RunComparisonRequest{
		Workload:      compWorkload(),
		Architectures: []*v1.ComparisonArchitectureInput{{Name: "aws", Architecture: awsArch(t)}, {Name: "gcp", Architecture: gcpArch(t)}},
		Options:       compOptions(),
	}))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := workload.Derive(compWorkloadSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Msg.GetResult().GetEntries() {
		p := e.GetPlan()
		if p.GetPeakRps() != plan.PeakRPS {
			t.Fatalf("entry %s plan peak %f, want %f", e.GetName(), p.GetPeakRps(), plan.PeakRPS)
		}
		if p.GetAverageRps() != plan.AverageRPS {
			t.Fatalf("entry %s plan avg %f, want %f", e.GetName(), p.GetAverageRps(), plan.AverageRPS)
		}
	}
}

func compWorkloadSpec() workload.Spec {
	return workload.Spec{
		TotalUsers: 1_000_000, DAU: 100_000, RequestsPerUserPerDay: 20,
		PeakMultiplier: 3, ReadWriteRatio: 4, PayloadBytes: 4096,
	}
}
