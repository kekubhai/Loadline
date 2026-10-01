package loadlinev1

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/kekubhai/Loadline/apps/simulator/internal/arena"
	"github.com/kekubhai/Loadline/apps/simulator/internal/arena/scoring"
	"github.com/kekubhai/Loadline/apps/simulator/internal/workspace"
	v1 "github.com/kekubhai/Loadline/apps/simulator/loadline/v1"
	"github.com/kekubhai/Loadline/apps/simulator/workload"
)

// This file adapts the Arena domain (internal/arena) to the API messages. It
// never computes a benchmark number: every value is copied from a definition
// or from a result the engine already produced.

// definitionToProto renders a full challenge definition, including exactly
// how submissions are evaluated.
func definitionToProto(d arena.Definition) *v1.Challenge {
	return &v1.Challenge{
		Summary:      summaryToProto(d),
		Requirements: requirementsToProto(d),
		Scoring:      weightsToProto(d.Scoring),
		Seed:         d.Seed,
		DurationMs:   durationOf(d),
	}
}

func summaryToProto(d arena.Definition) *v1.ChallengeSummary {
	return &v1.ChallengeSummary{
		Slug:        d.Slug,
		Version:     int32(d.Version),
		Name:        d.Name,
		Description: d.Description,
		Difficulty:  string(d.Difficulty),
		Category:    string(d.Category),
		Status:      string(d.Status),
		Workload:    workloadSpecToProto(d.Workload.Spec()),
		Constraints: constraintsToProto(d.Constraints),
		Failures:    failureScenariosToProto(d.Failures),
	}
}

func constraintsToProto(c arena.Constraints) *v1.ChallengeConstraints {
	return &v1.ChallengeConstraints{
		TargetThroughputRps: c.TargetThroughputRPS,
		TargetP95Ms:         c.TargetP95MS,
		TargetP99Ms:         c.TargetP99MS,
		TargetErrorRate:     c.TargetErrorRate,
		MonthlyBudgetUsd:    c.MonthlyBudgetUSD,
	}
}

func weightsToProto(w arena.Weights) *v1.ScoringWeights {
	return &v1.ScoringWeights{
		Throughput:      w.Throughput,
		Latency:         w.Latency,
		Reliability:     w.Reliability,
		Cost:            w.Cost,
		FailureRecovery: w.FailureRecovery,
	}
}

func requirementsToProto(d arena.Definition) []*v1.KindRequirement {
	out := make([]*v1.KindRequirement, 0, len(d.Requirements.RequiredKinds))
	for _, r := range d.Requirements.RequiredKinds {
		out = append(out, &v1.KindRequirement{Kind: r.Kind, Min: int32(r.Min)})
	}
	return out
}

func failureScenariosToProto(fs []arena.FailureScenario) []*v1.FailureScenario {
	out := make([]*v1.FailureScenario, 0, len(fs))
	for _, f := range fs {
		out = append(out, &v1.FailureScenario{
			TargetKind:     f.TargetKind,
			Type:           f.Type,
			StartMs:        f.StartMS,
			DurationMs:     f.DurationMS,
			AddedLatencyMs: f.AddedLatencyMS,
			ErrorRate:      f.ErrorRate,
			PacketLossRate: f.PacketLossRate,
			PassThrough:    f.PassThrough,
		})
	}
	return out
}

// workloadSpecToProto converts the engine's workload spec into API shape.
func workloadSpecToProto(s workload.Spec) *v1.WorkloadSpec {
	return &v1.WorkloadSpec{
		TotalUsers:            s.TotalUsers,
		Dau:                   s.DAU,
		RequestsPerUserPerDay: s.RequestsPerUserPerDay,
		PeakMultiplier:        s.PeakMultiplier,
		ReadWriteRatio:        s.ReadWriteRatio,
		PayloadBytes:          s.PayloadBytes,
	}
}

// protoToWorkload converts an API workload back into the engine's spec.
func protoToWorkload(pb *v1.WorkloadSpec) workload.Spec {
	return workload.Spec{
		TotalUsers:            pb.GetTotalUsers(),
		DAU:                   pb.GetDau(),
		RequestsPerUserPerDay: pb.GetRequestsPerUserPerDay(),
		PeakMultiplier:        pb.GetPeakMultiplier(),
		ReadWriteRatio:        pb.GetReadWriteRatio(),
		PayloadBytes:          pb.GetPayloadBytes(),
	}
}

// scoreBreakdownToProto renders the explainable score.
func scoreBreakdownToProto(r scoring.Result) *v1.ScoreBreakdown {
	out := &v1.ScoreBreakdown{
		Score:      r.Score,
		Components: make([]*v1.ScoreComponent, 0, len(r.Components)),
		Positives:  append([]string(nil), r.Positives...),
		Negatives:  append([]string(nil), r.Negatives...),
	}
	for _, c := range r.Components {
		out.Components = append(out.Components, &v1.ScoreComponent{
			Name:     c.Name,
			Score:    c.Score,
			Weight:   c.Weight,
			Weighted: c.Weighted,
			Detail:   c.Detail,
			Evidence: append([]string(nil), c.Evidence...),
		})
	}
	return out
}

func durationOf(d arena.Definition) float64 {
	if d.Workload.DurationMS <= 0 {
		return arena.DefaultDurationMS
	}
	return d.Workload.DurationMS
}

// submissionToProto assembles a submission message from its stored record
// and the server-decoded payloads. rank is 0 for a failed submission.
func submissionToProto(s workspace.ChallengeSubmission, rank int64) (*v1.Submission, error) {
	plan, err := unmarshalPlan(s.Plan)
	if err != nil {
		return nil, err
	}
	metrics, err := unmarshalMetrics(s.Metrics)
	if err != nil {
		return nil, err
	}
	diagnosis, err := unmarshalDiagnosis(s.Bottlenecks)
	if err != nil {
		return nil, err
	}
	capacity, err := unmarshalCapacity(s.Capacity)
	if err != nil {
		return nil, err
	}
	cost, err := unmarshalCost(s.Cost)
	if err != nil {
		return nil, err
	}
	breakdown, err := unmarshalBreakdown(s.ScoreBreakdown)
	if err != nil {
		return nil, err
	}
	return &v1.Submission{
		Id:               s.ID,
		ChallengeSlug:    s.ChallengeSlug,
		ChallengeVersion: int32(s.ChallengeVersion),
		DisplayName:      s.DisplayName,
		Status:           string(s.Status),
		Score:            s.Score,
		Rank:             rank,
		Plan:             plan,
		Metrics:          metrics,
		Diagnosis:        diagnosis,
		Capacity:         capacity,
		Cost:             cost,
		Breakdown:        breakdown,
		Seed:             s.Seed,
		Error:            s.Error,
		SubmittedAt:      timestampFromTime(s.SubmittedAt),
	}, nil
}

// unmarshalBreakdown decodes a stored score breakdown.
func unmarshalBreakdown(raw []byte) (*v1.ScoreBreakdown, error) {
	if isEmptyJSON(raw) {
		return nil, nil
	}
	var m v1.ScoreBreakdown
	if err := protojson.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("arena: decode score breakdown: %w", err)
	}
	return &m, nil
}
