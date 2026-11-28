package sleep

import (
	"testing"
	"time"

	"domestique/internal/load"
	"domestique/internal/store"
)

// nightsWaking builds n nights in a row, ending day(n), each waking at wake,
// asleep for h hours at 90% efficiency, with a 7.5 h baseline.
func nightsWaking(n int, wake string, h float64) []store.Sleep {
	w, _ := time.Parse("15:04", wake)
	var out []store.Sleep
	for i := 1; i <= n; i++ {
		d, _ := time.ParseInLocation("2006-01-02", day(i), utc)
		end := time.Date(d.Year(), d.Month(), d.Day(), w.Hour(), w.Minute(), 0, 0, utc)
		asleep := int(h * 3600)
		eff := 90
		out = append(out, store.Sleep{ID: day(i), Date: day(i), StartAt: end.Add(-time.Duration(float64(asleep)/0.9) * time.Second).UnixMilli(),
			EndAt: end.UnixMilli(), AsleepS: asleep, Efficiency: &eff, Need: &store.SleepNeed{BaselineS: 27000}})
	}
	return out
}

func TestBedtimeUsualNight(t *testing.T) {
	// Seven-and-a-half hours a night: nothing owed, need is the baseline.
	b := PlanBedtime(BedtimeInput{Date: day(14), Nights: nightsWaking(14, "06:30", 7.5), Loc: utc})
	if b == nil {
		t.Fatal("no bedtime")
	}
	// 7:30 at 90% is 8:20 in bed: 06:30 - 8:20 = 22:10.
	if b.Wake != "06:30" || b.WakeFrom != "usual" || b.NeedS != 27000 || b.BedBy != "22:10" || b.DebtS != 0 {
		t.Fatalf("%+v", b)
	}
	if b.Why != "Your usual need, nothing owed this week." {
		t.Fatalf("why %q", b.Why)
	}
}

func TestBedtimeDebtAndHeavyDay(t *testing.T) {
	// 6 h a night: 1.5 h short each, 10.5 h over the week; a third is
	// capped at an hour. Plus half an hour after a heavy day.
	b := PlanBedtime(BedtimeInput{Date: day(14), Nights: nightsWaking(14, "06:30", 6),
		Today: &load.Day{Date: day(14), TSS: 130, CTL: 60}, Loc: utc})
	if b.DebtS != DebtCapS || b.HeavyS != HeavyS || b.NeedS != 27000+3600+1800 {
		t.Fatalf("need %+v", b)
	}
	// 9:00 at 90% is 10:00 in bed: 20:30.
	if b.BedBy != "20:30" || b.Why != "Hard training day, and 10:30 short this week." {
		t.Fatalf("%s %q", b.BedBy, b.Why)
	}
}

func TestBedtimeWakeSetInSettings(t *testing.T) {
	// A wake time set for tomorrow's weekday wins over the usual.
	eve, _ := time.ParseInLocation("2006-01-02", day(14), utc)
	key := Weekdays[eve.AddDate(0, 0, 1).Weekday()]
	b := PlanBedtime(BedtimeInput{Date: day(14), Nights: nightsWaking(14, "06:30", 7.5), Wake: map[string]string{key: "05:45"}, Loc: utc})
	if b.Wake != "05:45" || b.WakeFrom != "set" || b.BedBy != "21:25" {
		t.Fatalf("%+v", b)
	}
	if PlanBedtime(BedtimeInput{Date: day(14), Loc: utc}) != nil {
		t.Fatal("bedtime with no nights")
	}
}

func TestBedtimeAfterMidnight(t *testing.T) {
	// A late wake puts bed past midnight: 09:00 - 8:20 = 00:40.
	eve, _ := time.ParseInLocation("2006-01-02", day(14), utc)
	key := Weekdays[eve.AddDate(0, 0, 1).Weekday()]
	b := PlanBedtime(BedtimeInput{Date: day(14), Nights: nightsWaking(14, "06:30", 7.5), Wake: map[string]string{key: "09:00"}, Loc: utc})
	if b == nil || b.BedBy != "00:40" {
		t.Fatalf("%+v", b)
	}
}

func TestCheckEstimates(t *testing.T) {
	ns := nightsWaking(4, "06:30", 7.5) // need 7:30 each
	est := []Estimate{{Date: day(1), NeedS: 26000}, {Date: day(2), NeedS: 28000}, {Date: day(3), NeedS: 27500}, {Date: day(9), NeedS: 1}}
	c := CheckEstimates(est, ns, utc)
	if c.Nights != 3 || c.OffS != -500 {
		t.Fatalf("%+v", c)
	}
}
