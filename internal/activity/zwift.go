package activity

// Zwift draws its worlds onto real coordinates. Some sit on the place they
// copy (London over London); others are made up and sit over open ocean in the
// South Pacific (Watopia near Vanuatu, France near New Caledonia). A map of the
// real place under a made-up world shows water or unrelated land, so the map
// needs to know which world a route is in.

// ZwiftWorld is a Zwift world a route was ridden in.
type ZwiftWorld struct {
	Name string `json:"name"`
	// Real is true when the world sits on the place it copies, so a real map
	// under it shows the right place, even if the roads only roughly match.
	Real bool `json:"real"`
}

type worldBox struct {
	ZwiftWorld
	minLat, maxLat, minLng, maxLng float64
}

// zwiftWorlds are drawn a little wider than the roads seen in recorded rides,
// so new routes in the same world still land inside. No two boxes are near
// each other, so the margin can't make one world read as another.
var zwiftWorlds = []worldBox{
	{ZwiftWorld{"Watopia", false}, -11.80, -11.58, 166.85, 167.05},
	{ZwiftWorld{"Makuri Islands", false}, -10.88, -10.68, 165.74, 165.92},
	{ZwiftWorld{"Crit City", false}, -10.42, -10.35, 165.76, 165.84},
	{ZwiftWorld{"France", false}, -21.82, -21.64, 166.08, 166.27},
	// Scotland's roads are made up, laid over the Isle of Arran.
	{ZwiftWorld{"Scotland", false}, 55.58, 55.72, -5.32, -5.12},
	{ZwiftWorld{"Richmond", true}, 37.48, 37.60, -77.52, -77.37},
	{ZwiftWorld{"New York", true}, 40.73, 40.83, -74.01, -73.92},
	{ZwiftWorld{"London", true}, 51.40, 51.54, -0.22, -0.03},
	{ZwiftWorld{"Innsbruck", true}, 47.18, 47.31, 11.33, 11.49},
	{ZwiftWorld{"Paris", true}, 48.84, 48.89, 2.27, 2.35},
	{ZwiftWorld{"Yorkshire", true}, 53.93, 54.04, -1.66, -1.49},
	{ZwiftWorld{"Bologna", true}, 44.46, 44.52, 11.26, 11.38},
}

// ZwiftWorldOf is the Zwift world whose box holds the middle of the route
// ([lat, lng] points), or nil when the route isn't in one.
func ZwiftWorldOf(route [][2]float64) *ZwiftWorld {
	if len(route) == 0 {
		return nil
	}
	minLat, maxLat, minLng, maxLng := route[0][0], route[0][0], route[0][1], route[0][1]
	for _, p := range route[1:] {
		minLat, maxLat = min(minLat, p[0]), max(maxLat, p[0])
		minLng, maxLng = min(minLng, p[1]), max(maxLng, p[1])
	}
	lat, lng := (minLat+maxLat)/2, (minLng+maxLng)/2
	for _, w := range zwiftWorlds {
		if lat >= w.minLat && lat <= w.maxLat && lng >= w.minLng && lng <= w.maxLng {
			return &w.ZwiftWorld
		}
	}
	return nil
}
