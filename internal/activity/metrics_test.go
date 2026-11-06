package activity

import "testing"

// A run of missing power samples is a gap in time, not samples to drop: a
// 30s dropout must split the segment (or be held, if short), never splice the
// two sides together as if they were contiguous.
func TestResampleMissingRun(t *testing.T) {
	var ts, pw []int32
	for i := int32(0); i < 100; i++ {
		ts = append(ts, i)
		switch {
		case i < 40:
			pw = append(pw, 100)
		case i < 70:
			pw = append(pw, Missing) // 30s dropout
		default:
			pw = append(pw, 300)
		}
	}
	d := resample(ts, pw)
	if len(d.segs) != 2 || len(d.segs[0]) != 40 || len(d.segs[1]) != 30 {
		t.Fatalf("want segments of 40 and 30, got %d segs", len(d.segs))
	}

	// A short dropout (5s) is held at the last value.
	pw2 := make([]int32, 20)
	for i := range pw2 {
		pw2[i] = 200
	}
	for i := 5; i < 10; i++ {
		pw2[i] = Missing
	}
	d = resample(ts[:20], pw2)
	if len(d.segs) != 1 || len(d.segs[0]) != 20 {
		t.Fatalf("short gap: want one segment of 20, got %v", d.segs)
	}
}

func TestParseTCXFractionalSpeed(t *testing.T) {
	tcx := []byte(`<?xml version="1.0"?><TrainingCenterDatabase xmlns:ns3="x"><Activities><Activity Sport="Biking"><Lap><Track>
<Trackpoint><Time>2024-05-01T10:00:00Z</Time><Extensions><ns3:TPX><ns3:Speed>5.75</ns3:Speed><ns3:Watts>210</ns3:Watts></ns3:TPX></Extensions></Trackpoint>
<Trackpoint><Time>2024-05-01T10:00:01Z</Time><Extensions><ns3:TPX><ns3:Speed>6.25</ns3:Speed><ns3:Watts>220</ns3:Watts></ns3:TPX></Extensions></Trackpoint>
</Track></Lap></Activity></Activities></TrainingCenterDatabase>`)
	p, err := ParseTCX(tcx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Stream.Speed) != 2 || p.Stream.Speed[0] != 5.75 || p.Stream.Speed[1] != 6.25 || p.Stream.Power[1] != 220 {
		t.Fatalf("speed %v power %v", p.Stream.Speed, p.Stream.Power)
	}
}

// Strain doesn't scale with duration: a short, hard workout must not score
// more than an hour at threshold per hour.
func TestStrainLoadCappedByDuration(t *testing.T) {
	if got := StrainLoad(14.3, 45*60); got != 75 {
		t.Errorf("45 min at strain 14.3 = %v, want capped at 75", got)
	}
	if got := StrainLoad(14, 2*3600); got != 100 {
		t.Errorf("2 h at strain 14 = %v, want 100 (under the cap)", got)
	}
	if got := StrainLoad(9, 0); got != 41.3 {
		t.Errorf("no duration = %v, want uncapped 41.3", got)
	}
}

func TestZoneHRHist(t *testing.T) {
	// Max 189, resting 59: a reserve of 130 bpm.
	h := ZoneHRHist([6]int{60, 0, 600, 0, 0, 30}, 189, 59)
	if h[118] != 60 || h[144] != 600 || h[183] != 30 {
		t.Fatalf("zone midpoints wrong: 118=%d 144=%d 183=%d", h[118], h[144], h[183])
	}
	if b := WhoopZoneBounds(189, 59); b != [5]int{124, 137, 150, 163, 176} {
		t.Fatalf("bounds = %v", b)
	}
	// 38 minutes almost all in Whoop zone 1 (a real indoor ride averaging
	// 125 bpm) scores like its heart rate stream did (24), not half that.
	tss := HRTSS(ZoneHRHist([6]int{0, 2330, 9, 0, 0, 0}, 189, 59), 59, 170)
	if tss < 20 || tss > 30 {
		t.Errorf("38 min in zone 1 = %v TSS, want 20-30", tss)
	}
	if ZoneHRHist([6]int{}, 189, 59) != nil || ZoneHRHist([6]int{0, 10}, 0, 59) != nil {
		t.Error("empty zones or unknown max HR should give no histogram")
	}
}
