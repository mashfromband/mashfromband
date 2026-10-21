package activity

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMergePreservesEditorialChangesAndExclusion(t *testing.T) {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	old := Data{Version: 1, Updated: now.Add(-time.Hour), Entries: []Entry{{ID: "github:1:pull:5", Title: "編集した説明", Summary: "手編集", Technologies: []string{"rust"}}, {ID: "github:1:pull:6", Title: "除外対象"}}}
	incoming := []Entry{{ID: "github:1:pull:5", Title: "APIの元の文", Technologies: []string{"go"}}, {ID: "github:1:pull:6"}, {ID: "github:1:release:5", Title: "正式公開", Date: now}}
	got, err := Merge(old, incoming, []Exclusion{{ID: "github:1:pull:6", Reason: "利用者向け実績ではない"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("entries = %#v", got.Entries)
	}
	var edited Entry
	for _, e := range got.Entries {
		if e.ID == old.Entries[0].ID {
			edited = e
		}
	}
	if !reflect.DeepEqual(edited, old.Entries[0]) {
		t.Fatalf("editorial changes lost: %#v", edited)
	}
	again, err := Merge(got, incoming, []Exclusion{{ID: "github:1:pull:6", Reason: "利用者向け実績ではない"}}, now.Add(24*time.Hour))
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("identical refresh changed data: %#v %v", again, err)
	}
}

func TestReplaceBlockPreservesHandwrittenBytesAndRejectsMalformedMarkers(t *testing.T) {
	before := "# 私の紹介\r\n日本語 English Русский\r\n![集計](assets/private-contributions.svg)\r\n"
	after := "\r\n## Operating Principles\r\n手書き\r\n"
	got, err := ReplaceBlock(before+Start+"\nold\n"+End+after, "new")
	if err != nil || got != before+Start+"\nnew\n"+End+after {
		t.Fatalf("unexpected result: %q %v", got, err)
	}
	for _, bad := range []string{"no markers", End + Start, Start + End + Start + End} {
		if _, err := ReplaceBlock(bad, "new"); err == nil {
			t.Errorf("accepted malformed marker input %q", bad)
		}
	}
}

func TestDeadlineUsesCreationTimeAndHold(t *testing.T) {
	now := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	created := now.Add(-7 * 24 * time.Hour)
	if !Ready(created, nil, false, now, 7*24*time.Hour) {
		t.Fatal("creation deadline did not become due")
	}
	if Ready(created, nil, false, now.Add(-time.Nanosecond), 7*24*time.Hour) {
		t.Fatal("premature publication")
	}
	for _, label := range []string{"hold", "HOLD"} {
		if Ready(created, []string{label}, false, now, 0) {
			t.Fatal("hold ignored")
		}
	}
	if Ready(created, nil, true, now, 0) || Ready(now.Add(time.Hour), nil, false, now, 0) {
		t.Fatal("draft/future creation accepted")
	}
}

func TestExclusionNeedsReason(t *testing.T) {
	_, err := Merge(Data{}, nil, []Exclusion{{ID: "x", Reason: "  "}}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatal("missing exclusion reason was not rejected")
	}
}
