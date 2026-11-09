package activity

// Zwift draws its worlds onto real coordinates. Some sit on the place they
// copy (London over London); others are made up and sit over open ocean in the
// South Pacific (Watopia near Vanuatu, France near New Caledonia). A map of the
// real place under a made-up world shows water or unrelated land, so the map
// needs to know which world a route is in.

// ZwiftWorld is a Zwift world a route was ridden in.
type ZwiftWorld struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
	// Real is true when the world sits on the place it copies, so a real map
	// under it shows the right place, even if the roads only roughly match.
	Real bool `json:"real"`
	// MapCorners place Zwift's map image of the world: the [lat, lng] of its
	// top-left, top-right, bottom-right and bottom-left corners. Some images
	// are drawn turned 90° from north, so it takes all four. Nil when there's
	// no image known to line up with recorded routes.
	MapCorners *[4][2]float64 `json:"map_corners,omitempty"`
	// Background is the colour at the edges of that image (sea or land), to
	// fill the map beyond it.
	Background string `json:"background,omitempty"`
}

type worldBox struct {
	ZwiftWorld
	minLat, maxLat, minLng, maxLng float64
	imageURL                       string // Zwift's map image; "" with no MapCorners
}

// corners of an image drawn north up, from its north-west and south-east.
func northUp(n, w, s, e float64) *[4][2]float64 {
	return &[4][2]float64{{n, w}, {n, e}, {s, e}, {s, w}}
}

// turned is an image drawn with north to the right (turned 90° anticlockwise).
func turned(n, w, s, e float64) *[4][2]float64 {
	return &[4][2]float64{{s, w}, {n, w}, {n, e}, {s, e}}
}

// zwiftWorlds are drawn a little wider than the roads seen in recorded rides,
// so new routes in the same world still land inside. No two boxes are near
// each other, so the margin can't make one world read as another.
//
// Image URLs and image extents are from zwift-data (MIT licence, Copyright (c)
// 2021 Andi Pätzold, https://github.com/andipaetzold/zwift-data). Each was
// checked by drawing recorded routes on Zwift's image: Watopia's is drawn
// north up, Makuri Islands' and Scotland's turned 90°, and all three put the
// routes on the drawn roads. France's image doesn't match its routes at any
// turn, and Crit City had no rides to check against, so both stay without
// one. The images are Zwift's artwork: they're fetched from Zwift at run time
// and never kept in this repository.
var zwiftWorlds = []worldBox{
	{ZwiftWorld{"Watopia", "watopia", false, northUp(-11.62597, 166.87747, -11.74087, 167.03255), "#0886e4"},
		-11.80, -11.58, 166.85, 167.05, "https://cdn.zwift.com/static/images/maps/MiniMap_Watopia_2.png"},
	{ZwiftWorld{"Makuri Islands", "makuri-islands", false, turned(-10.73746, 165.76591, -10.85234, 165.88222), "#0886e4"},
		-10.88, -10.68, 165.74, 165.92, "https://cdn.zwift.com/static/images/maps/MiniMap_Japan.png"},
	{ZwiftWorld{"Crit City", "crit-city", false, nil, ""}, -10.42, -10.35, 165.76, 165.84, ""},
	{ZwiftWorld{"France", "france", false, nil, ""}, -21.82, -21.64, 166.08, 166.27, ""},
	// Scotland's roads are made up, laid over the Isle of Arran.
	{ZwiftWorld{"Scotland", "scotland", false, turned(55.67595, -5.2802, 55.61845, -5.17798), "#2fc0c3"},
		55.58, 55.72, -5.32, -5.12, "https://cdn.zwift.com/static/images/maps/MiniMap_Scotland.png"},
	// Worlds on the place they copy keep the real map, so need no image.
	{ZwiftWorld{"Richmond", "richmond", true, nil, ""}, 37.48, 37.60, -77.52, -77.37, ""},
	{ZwiftWorld{"New York", "new-york", true, nil, ""}, 40.73, 40.83, -74.01, -73.92, ""},
	{ZwiftWorld{"London", "london", true, nil, ""}, 51.40, 51.54, -0.22, -0.03, ""},
	{ZwiftWorld{"Innsbruck", "innsbruck", true, nil, ""}, 47.18, 47.31, 11.33, 11.49, ""},
	{ZwiftWorld{"Paris", "paris", true, nil, ""}, 48.84, 48.89, 2.27, 2.35, ""},
	{ZwiftWorld{"Yorkshire", "yorkshire", true, nil, ""}, 53.93, 54.04, -1.66, -1.49, ""},
	{ZwiftWorld{"Bologna", "bologna", true, nil, ""}, 44.46, 44.52, 11.26, 11.38, ""},
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

// ZwiftMapURL is where Zwift serves its map image of the world with this
// slug, or "" when there's no image for it. Only these URLs are ever fetched.
func ZwiftMapURL(slug string) string {
	for _, w := range zwiftWorlds {
		if w.Slug == slug {
			return w.imageURL
		}
	}
	return ""
}
