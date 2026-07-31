package main

import (
	"testing"
)

func TestDeriveGitURL(t *testing.T) {
	tests := []struct {
		accountURL string
		want       string
		wantErr    bool
	}{
		// Table from Gitea-URL-Formula.md
		{
			accountURL: "https://admin.dev.corezoid.com",
			want:       "https://git-dev.dev.corezoid.com/corezoid-dev",
		},
		{
			accountURL: "https://admin-pre.corezoid.com",
			want:       "https://git-pre.pre.corezoid.com/corezoid-pre",
		},
		{
			accountURL: "https://admin.corezoid.com",
			want:       "https://git-prod.prod.corezoid.com/corezoid-prod",
		},
		{
			accountURL: "https://corezoid.leobank.az",
			want:       "https://git-prod.prod.leobank.az/corezoid-prod",
		},
		{
			accountURL: "https://corezoid-lq.leobank.az",
			want:       "https://git-lq.lq.leobank.az/corezoid-lq",
		},
		{
			accountURL: "https://corezoid.staging.liobank.vn",
			want:       "https://git-staging.staging.liobank.vn/corezoid-staging",
		},
		{
			accountURL: "https://corezoid.tezbank.uz",
			want:       "https://git-prod.prod.tezbank.uz/corezoid-prod",
		},
		{
			accountURL: "https://corezoid-ach.tezbank.uz",
			want:       "https://git-ach.ach.tezbank.uz/corezoid-ach",
		},
		// No scheme — should still work
		{
			accountURL: "admin.dev.corezoid.com",
			want:       "https://git-dev.dev.corezoid.com/corezoid-dev",
		},
		// Unknown first label — should error
		{
			accountURL: "https://account.corezoid.com",
			wantErr:    true,
		},
		// Empty input — should error
		{
			accountURL: "",
			wantErr:    true,
		},
		// Only 1 label — should error
		{
			accountURL: "https://localhost",
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.accountURL, func(t *testing.T) {
			got, err := deriveGitURL(tc.accountURL)
			if tc.wantErr {
				if err == nil {
					t.Errorf("deriveGitURL(%q) = %q, want error", tc.accountURL, got)
				}
				return
			}
			if err != nil {
				t.Errorf("deriveGitURL(%q) unexpected error: %v", tc.accountURL, err)
				return
			}
			if got != tc.want {
				t.Errorf("deriveGitURL(%q)\n got  %q\n want %q", tc.accountURL, got, tc.want)
			}
		})
	}
}
