package activity

import "testing"

func TestZwiftWorldOf(t *testing.T) {
	// Each route is two corners of a box of roads seen in recorded rides.
	for _, c := range []struct {
		name  string
		route [][2]float64
		want  string // "" = not in a Zwift world
		real  bool
	}{
		{"Watopia", [][2]float64{{-11.722, 166.922}, {-11.645, 166.995}}, "Watopia", false},
		{"Makuri Islands", [][2]float64{{-10.817, 165.800}, {-10.744, 165.857}}, "Makuri Islands", false},
		{"France", [][2]float64{{-21.745, 166.148}, {-21.698, 166.203}}, "France", false},
		{"Scotland", [][2]float64{{55.635, -5.245}, {55.659, -5.214}}, "Scotland", false},
		{"Richmond", [][2]float64{{37.520, -77.468}, {37.558, -77.416}}, "Richmond", true},
		{"New York", [][2]float64{{40.764, -73.982}, {40.798, -73.950}}, "New York", true},
		{"London", [][2]float64{{51.485, -0.164}, {51.511, -0.073}}, "London", true},
		{"Innsbruck", [][2]float64{{47.215, 11.386}, {47.281, 11.446}}, "Innsbruck", true},
		{"Paris", [][2]float64{{48.861, 2.294}, {48.874, 2.332}}, "Paris", true},
		{"Yorkshire", [][2]float64{{53.975, -1.593}, {54.000, -1.539}}, "Yorkshire", true},
		{"an outdoor ride elsewhere", [][2]float64{{39.74, -104.99}, {39.80, -104.90}}, "", false},
		{"a ride near, but not in, London", [][2]float64{{51.75, -1.26}, {51.76, -1.25}}, "", false},
		{"no route", nil, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ZwiftWorldOf(c.route)
			if c.want == "" {
				if got != nil {
					t.Fatalf("got %+v, want none", *got)
				}
				return
			}
			if got == nil || got.Name != c.want || got.Real != c.real {
				t.Fatalf("got %+v, want %s (real %v)", got, c.want, c.real)
			}
		})
	}
}

// The boxes must not overlap, or the order of the table would decide which
// world a route is in.
func TestZwiftWorldsApart(t *testing.T) {
	for i, a := range zwiftWorlds {
		if a.minLat >= a.maxLat || a.minLng >= a.maxLng {
			t.Errorf("%s: empty box", a.Name)
		}
		for _, b := range zwiftWorlds[i+1:] {
			if a.minLat <= b.maxLat && b.minLat <= a.maxLat && a.minLng <= b.maxLng && b.minLng <= a.maxLng {
				t.Errorf("%s and %s overlap", a.Name, b.Name)
			}
		}
	}
}
