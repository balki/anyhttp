package anyhttp

import (
	"encoding/json"
	"testing"
	"time"
)

func Test_parseAddress(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		addr         string
		wantAddrType AddressType
		wantUsc      *UnixSocketConfig
		wantSysc     *SysdConfig
		wantErr      bool
	}{
		{
			name:         "tcp port",
			addr:         ":8080",
			wantAddrType: TCP,
			wantUsc:      nil,
			wantSysc:     nil,
			wantErr:      false,
		},
		{
			name:         "unix address",
			addr:         "unix?path=/run/foo.sock&mode=660",
			wantAddrType: UnixSocket,
			wantUsc: &UnixSocketConfig{
				SocketPath:     "/run/foo.sock",
				SocketMode:     0660,
				RemoveExisting: true,
			},
			wantSysc: nil,
			wantErr:  false,
		},
		{
			name:         "systemd address",
			addr:         "sysd?name=foo.socket",
			wantAddrType: SystemdFD,
			wantUsc:      nil,
			wantSysc: &SysdConfig{
				FDIndex:     nil,
				FDName:      ptr("foo.socket"),
				CheckPID:    true,
				UnsetEnv:    true,
				IdleTimeout: nil,
			},
			wantErr: false,
		},
		{
			name:         "systemd address with index",
			addr:         "sysd?idx=0&idle_timeout=30m",
			wantAddrType: SystemdFD,
			wantUsc:      nil,
			wantSysc: &SysdConfig{
				FDIndex:     ptr(0),
				FDName:      nil,
				CheckPID:    true,
				UnsetEnv:    true,
				IdleTimeout: ptr(30 * time.Minute),
			},
			wantErr: false,
		},
		{
			name:         "systemd address. Bad example",
			addr:         "sysd?idx=0&idle_timeout=30m&name=foo",
			wantAddrType: SystemdFD,
			wantUsc:      nil,
			wantSysc:     nil,
			wantErr:      true,
		},
		{
			name:         "systemd address with check_pid and unset_env",
			addr:         "sysd?idx=0&check_pid=false&unset_env=f",
			wantAddrType: SystemdFD,
			wantUsc:      nil,
			wantSysc: &SysdConfig{
				FDIndex:     ptr(0),
				FDName:      nil,
				CheckPID:    false,
				UnsetEnv:    false,
				IdleTimeout: nil,
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAddrType, gotUsc, gotSysc, gotErr := parseAddress(tt.addr)
			if gotErr != nil {
				if !tt.wantErr {
					t.Errorf("parseAddress() failed: %v", gotErr)
				}
				return
			}
			if tt.wantErr {
				t.Fatal("parseAddress() succeeded unexpectedly")
			}

			if gotAddrType != tt.wantAddrType {
				t.Errorf("parseAddress() addrType = %v, want %v", gotAddrType, tt.wantAddrType)
			}

			if !check(gotUsc, tt.wantUsc) {
				t.Errorf("parseAddress() Usc = %v, want %v", gotUsc, tt.wantUsc)
			}
			if !check(gotSysc, tt.wantSysc) {
				if (gotSysc == nil || tt.wantSysc == nil) ||
					!(check(gotSysc.FDIndex, tt.wantSysc.FDIndex) &&
						check(gotSysc.FDName, tt.wantSysc.FDName) &&
						check(gotSysc.IdleTimeout, tt.wantSysc.IdleTimeout)) {
					t.Errorf("parseAddress() Sysc = %v, want %v", asJSON(gotSysc), asJSON(tt.wantSysc))
				}
			}
		})
	}
}

// Helpers

// print value instead of pointer
func asJSON[T any](val T) string {
	op, err := json.Marshal(val)
	if err != nil {
		return err.Error()
	}
	return string(op)
}

func ptr[T any](val T) *T {
	return &val
}

// nil safe equal check
func check[T comparable](got, want *T) bool {
	if (got == nil) != (want == nil) {
		return false
	}
	if got == nil {
		return true
	}
	return *got == *want
}
