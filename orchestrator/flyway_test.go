package main

import (
	"strings"
	"testing"
)

const mig = "app/src/main/resources/db/migration/"

func TestCheckFlywayMigrations(t *testing.T) {
	cases := []struct {
		nome     string
		existing []string
		added    []string
		want     []string // trechos que cada violação deve conter
	}{
		{"sem flyway", []string{"pom.xml", "src/A.java"}, []string{"src/B.java"}, nil},
		{"versão maior passa",
			[]string{mig + "V1__a.sql", mig + "V2__b.sql"}, []string{mig + "V3__c.sql"}, nil},
		{"repetida reprova",
			[]string{mig + "V1__a.sql", mig + "V2__b.sql"}, []string{mig + "V2__outra.sql"},
			[]string{mig + "V2__outra.sql", "repetida", mig + "V2__b.sql", "última versão da branch-alvo: 2"}},
		{"menor que a última reprova",
			[]string{mig + "V1__a.sql", mig + "V5__b.sql"}, []string{mig + "V3__c.sql"},
			[]string{mig + "V3__c.sql", "menor", "5", mig + "V5__b.sql"}},
		{"repetida entre duas novas",
			[]string{mig + "V1__a.sql"}, []string{mig + "V2__x.sql", mig + "V2__y.sql"},
			[]string{mig + "V2__y.sql", "repetida", mig + "V2__x.sql"}},
		{"antigas sem sufixo e timestamp de 12 e 14 dígitos",
			[]string{mig + "V1__a.sql", mig + "V202604241230__b.sql", mig + "V20260424123000__c.sql"},
			[]string{mig + "V20260425090000_71__d.sql"}, nil},
		{"timestamp de 14 dígitos novo menor que o último",
			[]string{mig + "V20260424123000__c.sql"}, []string{mig + "V20260101000000__d.sql"},
			[]string{"menor"}},
		{"12 dígitos novo depois de 14 reprova",
			[]string{mig + "V20260424123000__c.sql"}, []string{mig + "V202604251200__d.sql"},
			[]string{"menor"}},
		{"zero à esquerda é a mesma versão",
			[]string{mig + "V1__a.sql"}, []string{mig + "V01__b.sql"}, []string{"repetida"}},
		{"1.1 e 1_1 são a mesma versão",
			[]string{mig + "V1_1__a.sql"}, []string{mig + "V1.1__b.sql"}, []string{"repetida"}},
		{"1.1 maior que 1",
			[]string{mig + "V1__a.sql"}, []string{mig + "V1_1__b.sql"}, nil},
		{"históricos por módulo são independentes",
			[]string{mig + "V9__a.sql", mig + "financeiro/V1__a.sql"},
			[]string{mig + "financeiro/V2__b.sql", mig + "compras/V1__a.sql"}, nil},
		{"módulo reprova pelo próprio histórico",
			[]string{mig + "V9__a.sql", mig + "financeiro/V5__a.sql"},
			[]string{mig + "financeiro/V4__b.sql"},
			[]string{mig + "financeiro/V4__b.sql", mig + "financeiro/V5__a.sql"}},
		{"repetível e fora de db/migration ignorados",
			[]string{mig + "V5__a.sql"},
			[]string{mig + "R__view.sql", "docs/V1__nota.sql", mig + "V2__b.txt"}, nil},
		{"primeira migração do projeto",
			nil, []string{mig + "V1__a.sql"}, nil},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			got := checkFlywayMigrations(c.existing, c.added)
			if c.want == nil {
				if len(got) != 0 {
					t.Fatalf("esperava aprovar, veio %v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("esperava 1 violação, veio %v", got)
			}
			for _, w := range c.want {
				if !strings.Contains(got[0], w) {
					t.Errorf("violação %q não contém %q", got[0], w)
				}
			}
		})
	}
}
