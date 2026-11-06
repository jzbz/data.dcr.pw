package dcrpg

import (
	"os"
	"testing"

	"github.com/lib/pq"
)

func TestConnString(t *testing.T) {
	tests := []struct {
		name                           string
		host, port, user, pass, dbname string
		sslFromEnv                     bool
		envSSLMode                     string
		wantPort                       uint16
		wantSSLMode                    pq.SSLMode
	}{
		{
			name: "unix socket, no password",
			host: "/run/postgresql", port: "5432", user: "dcrdata", dbname: "dcrdata",
			wantSSLMode: pq.SSLModeDisable,
		},
		{
			name: "tcp, password with spaces and quotes",
			host: "127.0.0.1", port: "5433", user: "dcr data", pass: `p@ss 'w\rd sslmode=require`,
			dbname: "dcrdata", wantPort: 5433, wantSSLMode: pq.SSLModeDisable,
		},
		{
			name: "tcp, sslmode from environment",
			host: "db.example.org", port: "5432", user: "dcrdata", pass: "secret",
			dbname: "dcrdata", sslFromEnv: true, envSSLMode: "verify-full",
			wantPort: 5432, wantSSLMode: pq.SSLModeVerifyFull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv restores the original value when the test ends.
			t.Setenv("PGSSLMODE", tt.envSSLMode)
			if tt.envSSLMode == "" {
				os.Unsetenv("PGSSLMODE")
			}
			dsn := connString(tt.host, tt.port, tt.user, tt.pass, tt.dbname, tt.sslFromEnv)
			cfg, err := pq.NewConfig(dsn)
			if err != nil {
				t.Fatalf("NewConfig(%q): %v", dsn, err)
			}
			if cfg.Host != tt.host {
				t.Errorf("host = %q, want %q", cfg.Host, tt.host)
			}
			if cfg.User != tt.user {
				t.Errorf("user = %q, want %q", cfg.User, tt.user)
			}
			if cfg.Password != tt.pass {
				t.Errorf("password = %q, want %q", cfg.Password, tt.pass)
			}
			if cfg.Database != tt.dbname {
				t.Errorf("dbname = %q, want %q", cfg.Database, tt.dbname)
			}
			if tt.wantPort != 0 && cfg.Port != tt.wantPort {
				t.Errorf("port = %d, want %d", cfg.Port, tt.wantPort)
			}
			if cfg.SSLMode != tt.wantSSLMode {
				t.Errorf("sslmode = %q, want %q", cfg.SSLMode, tt.wantSSLMode)
			}
		})
	}
}
