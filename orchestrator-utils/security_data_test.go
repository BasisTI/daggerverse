package main

import (
	"strings"
	"testing"
	"time"
)

func TestClassifySecurityData(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	warn, fail := 48*time.Hour, 168*time.Hour
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	cases := []struct {
		nome    string
		label   string
		want    dataFreshness
		wantErr bool
	}{
		{"0h", ago(0), freshnessOK, false},
		{"47h", ago(47 * time.Hour), freshnessOK, false},
		{"exatos 48h", ago(48 * time.Hour), freshnessOK, false},
		{"49h", ago(49 * time.Hour), freshnessWarn, false},
		{"exatos 7d", ago(fail), freshnessWarn, false},
		{"7d+1s", ago(fail + time.Second), freshnessStale, false},
		{"label ausente", "", freshnessStale, true},
		{"label só espaços", "  ", freshnessStale, true},
		{"label ilegível", "ontem", freshnessStale, true},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			got, _, err := classifySecurityData(c.label, now, warn, fail)
			if (err != nil) != c.wantErr {
				t.Fatalf("erro = %v, quer erro=%v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("faixa = %v, quer %v", got, c.want)
			}
		})
	}
}

func TestClassifySecurityDataIdade(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	_, age, err := classifySecurityData(now.Add(-50*time.Hour).Format(time.RFC3339), now, 48*time.Hour, 168*time.Hour)
	if err != nil || age != 50*time.Hour {
		t.Errorf("idade = %v, erro %v; quer 50h", age, err)
	}
}

func TestClassifySecurityDataErroNomeiaOLabel(t *testing.T) {
	_, _, err := classifySecurityData("", time.Now(), time.Hour, 2*time.Hour)
	if err == nil || !strings.Contains(err.Error(), SecurityDataLabel) {
		t.Errorf("erro = %v; quer menção ao label", err)
	}
}
