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

func TestZwiftWorldImages(t *testing.T) {
	slugs := map[string]bool{}
	images := 0
	for _, w := range zwiftWorlds {
		if w.Slug == "" || slugs[w.Slug] {
			t.Errorf("%s: missing or repeated slug %q", w.Name, w.Slug)
		}
		slugs[w.Slug] = true
		if (w.MapCorners == nil) != (w.imageURL == "") {
			t.Errorf("%s: an image needs both a URL and corners", w.Name)
		}
		if got := ZwiftMapURL(w.Slug); got != w.imageURL {
			t.Errorf("%s: ZwiftMapURL = %q", w.Name, got)
		}
		if w.MapCorners == nil {
			continue
		}
		images++
		if w.Real {
			t.Errorf("%s: a real world keeps the real map", w.Name)
		}
		if w.Background == "" {
			t.Errorf("%s: needs a background colour", w.Name)
		}
		// The corners must make a rectangle that covers the middle of the
		// world's box, or routes would be drawn off the map.
		minLat, maxLat, minLng, maxLng := 90.0, -90.0, 180.0, -180.0
		for _, c := range w.MapCorners {
			minLat, maxLat = min(minLat, c[0]), max(maxLat, c[0])
			minLng, maxLng = min(minLng, c[1]), max(maxLng, c[1])
		}
		c := w.MapCorners
		if c[0][0] != c[1][0] && c[0][1] != c[1][1] || c[2][0] != c[3][0] && c[2][1] != c[3][1] {
			t.Errorf("%s: corners %v aren't a rectangle in order", w.Name, *c)
		}
		lat, lng := (w.minLat+w.maxLat)/2, (w.minLng+w.maxLng)/2
		if lat < minLat || lat > maxLat || lng < minLng || lng > maxLng {
			t.Errorf("%s: image %v doesn't cover the world's middle", w.Name, *c)
		}
	}
	if images != 3 {
		t.Errorf("%d worlds have images, want Watopia, Makuri Islands and Scotland", images)
	}
	for _, s := range []string{"", "atlantis", "../watopia", "france", "london", "https://cdn.zwift.com/x.png"} {
		if got := ZwiftMapURL(s); got != "" {
			t.Errorf("ZwiftMapURL(%q) = %q, want none", s, got)
		}
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
