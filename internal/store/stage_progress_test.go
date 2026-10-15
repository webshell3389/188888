package store

import "testing"

func TestClientStageProgressForFiltersRequestedClients(t *testing.T) {
	progress := map[string]int{
		"10.0.0.1": 100,
		"10.0.0.2": 58,
		"10.0.0.3": 18,
	}

	got := filterClientStageProgress(progress, []string{"10.0.0.1", "10.0.0.3", "missing"})
	if len(got) != 2 || got["10.0.0.1"] != 100 || got["10.0.0.3"] != 18 {
		t.Fatalf("filtered progress = %#v", got)
	}
}

func TestClientStageProgressQueryUsesBoundParameters(t *testing.T) {
	query, args := clientStageProgressQuery([]string{"10.0.0.1", "10.0.0.2"})
	if query != "SELECT client, pct FROM client_stage WHERE pct > 0 AND client IN (?,?)" {
		t.Fatalf("query = %q", query)
	}
	if len(args) != 2 || args[0] != "10.0.0.1" || args[1] != "10.0.0.2" {
		t.Fatalf("args = %#v", args)
	}
}
