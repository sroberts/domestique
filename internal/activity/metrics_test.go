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
