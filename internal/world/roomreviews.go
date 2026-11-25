// internal/world/roomreviews.go
//
// A room's agent files a review page in the room (`aiagent review`), and the
// room's user checks it there. The admin sees every room's pages in the same
// list, marked with the room, and answers them the same way: the request is
// sent on to the room the page lives in, so the answer reaches that room's
// agent. This is how a request for the admin ("ready to merge, version
// bumped") comes back from a user's session.
package world

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

type roomReviewCache struct {
	sync.Mutex
	at    time.Time
	list  []Review
	owner map[string]string
}

const roomReviewTTL = 5 * time.Second

// roomReviews reads every room's reviews; owner maps a review id to its room.
func (s *Service) roomReviews(ctx context.Context) ([]Review, map[string]string) {
	if s.OSUsers == nil {
		return nil, nil
	}
	s.roomRev.Lock()
	defer s.roomRev.Unlock()
	if s.roomRev.owner != nil && time.Since(s.roomRev.at) < roomReviewTTL {
		return s.roomRev.list, s.roomRev.owner
	}
	var list []Review
	owner := map[string]string{}
	for _, name := range s.roomNames() {
		cctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		status, _, b, err := s.OSUsers.do(cctx, name, "GET", apiPrefix+"/reviews", "", nil)
		cancel()
		var env struct {
			Details struct {
				Reviews []Review `json:"reviews"`
			} `json:"details"`
		}
		if err != nil || status != 200 || json.Unmarshal(b, &env) != nil {
			continue
		}
		for _, r := range env.Details.Reviews {
			if !reviewIDRE.MatchString(r.ID) || owner[r.ID] != "" {
				continue
			}
			r.Owner, r.Cwd = name, ""
			owner[r.ID] = name
			list = append(list, r)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	s.roomRev.at, s.roomRev.list, s.roomRev.owner = time.Now(), list, owner
	return list, owner
}

// roomOfReview names the room a review lives in, or "" for one of this
// service's own.
func (s *Service) roomOfReview(ctx context.Context, id string) string {
	if !reviewIDRE.MatchString(id) {
		return ""
	}
	if _, err := s.Collector.review(id); err == nil {
		return ""
	}
	_, owner := s.roomReviews(ctx)
	return owner[id]
}
