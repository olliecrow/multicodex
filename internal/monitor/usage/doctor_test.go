package usage

import "testing"

func TestDoctorReportStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		report DoctorReport
		want   string
	}{
		{
			name: "healthy",
			report: DoctorReport{Checks: []DoctorCheck{
				{Name: "app-server fetch", OK: true},
				{Name: "oauth fetch", OK: true},
			}},
			want: "healthy",
		},
		{
			name: "degraded",
			report: DoctorReport{Checks: []DoctorCheck{
				{Name: "app-server fetch", OK: true},
				{Name: "oauth fetch", OK: false},
			}},
			want: "degraded",
		},
		{
			name: "failed",
			report: DoctorReport{Checks: []DoctorCheck{
				{Name: "app-server fetch", OK: false},
				{Name: "oauth fetch", OK: false},
			}},
			want: "failed",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.report.Status(); got != tc.want {
				t.Fatalf("Status() = %q, want %q", got, tc.want)
			}
			if got := tc.report.Healthy(); got != (tc.want != "failed") {
				t.Fatalf("Healthy() = %v, want %v", got, tc.want != "failed")
			}
		})
	}
}
