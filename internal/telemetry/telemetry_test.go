package telemetry

import (
	"testing"
	"time"
)

func TestRecordAndRead(t *testing.T) {
	rec := New(t.TempDir(), true)
	now := time.Now()
	if err := rec.Record(Record{Time: now, Provider: "openrouter", RoutedModel: "z-ai/glm-5.3-flash", Role: "worker"}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := rec.Record(Record{Time: now, Provider: "anthropic-native", Role: "architect"}); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := rec.ReadSince(now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d records, want 2", len(got))
	}
}

func TestDisabledIsNoop(t *testing.T) {
	rec := New(t.TempDir(), false)
	if err := rec.Record(Record{Provider: "x"}); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := rec.ReadSince(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no records when disabled")
	}
}
