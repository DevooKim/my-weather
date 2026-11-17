package timeline

import (
	"context"
	"time"

	"myweather/internal/kst"
	"myweather/internal/store"
)

// Load는 now 시점의 타임라인 재료를 DB에서 읽는다.
//
//   - 단기: base가 [어제 00시 − 28시간, now]인 발표분(각 최고 ver)의 값 중 어제 00시 이후 것.
//     어제 00시를 담는 그저께 23시·20시 발표분까지 들어간다.
//   - 초단기: 가장 늦은 발표분과 그 직전 발표분(지금 −7시간 이내). 직전 분은 지금 시각을 최신 분이 덮지 못할 때 강수 보정에 쓴다.
//   - 실황: 어제 00시 ~ 지금.
func Load(ctx context.Context, db *store.DB, place Place, now time.Time) (Input, error) {
	now = now.In(kst.Loc)
	in := Input{Now: now, Place: place}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, kst.Loc)
	start := today.AddDate(0, 0, -1)
	nx, ny := place.NX, place.NY

	vs, err := db.FcstRange(ctx, "vilage", nx, ny, kst.ToInt(start.Add(-28*time.Hour)), kst.ToInt(now), kst.ToInt(start))
	if err != nil {
		return in, err
	}
	for _, b := range vs {
		in.Vilage = append(in.Vilage, Fcst{Base: kst.FromInt(b.Base), Ver: b.Ver, Rows: b.Rows})
	}
	ub, err := db.LatestFcstBase(ctx, "ultra", nx, ny)
	if err != nil {
		return in, err
	}
	if ub > 0 && !kst.FromInt(ub).Before(now.Add(-7*time.Hour)) {
		from := kst.ToInt(kst.Ultra.Prev(kst.FromInt(ub)))
		if kst.FromInt(from).Before(now.Add(-7 * time.Hour)) {
			from = ub
		}
		us, err := db.FcstRange(ctx, "ultra", nx, ny, from, ub, 0)
		if err != nil {
			return in, err
		}
		for _, b := range us {
			in.Ultra = append(in.Ultra, Fcst{Base: kst.FromInt(b.Base), Ver: b.Ver, Rows: b.Rows})
		}
	}
	in.Obs, err = db.ObsRange(ctx, nx, ny, kst.ToInt(start), kst.ToInt(now))
	return in, err
}
