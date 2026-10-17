package main

// A static, server-rendered projection of the Stellar contract for GitHub
// README <img> embeds. This intentionally does not reproduce Three.js bloom,
// animation or parallax; it preserves genesis, ordered history, multi-arm disc,
// magnitude tiers, release markers and name-hashed branch asterisms.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"html"
	"math"
	"net/http"
	"strings"
)

const (
	badgeViewWidth  = 960
	badgeViewHeight = 320
	badgeCenterX    = 604.0
	badgeCenterY    = 160.0
)

type badgePoint struct{ x, y float64 }

// NewBadgeHandler adapts the existing (*server).build method directly:
//
//	mux.HandleFunc("/api/badge", NewBadgeHandler(s.build))
//
// Use ?repo=owner/name&size=compact|medium|large (medium is the default).
// The response is a static SVG that README image proxies can cache for five
// minutes, with stale-while-revalidate for up to an hour. For private repos,
// gate this public endpoint at the routing/auth layer before enabling embeds.
func NewBadgeHandler(build func(context.Context, string) ([]byte, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeErr(w, &apiError{status: http.StatusMethodNotAllowed, code: "method_not_allowed", msg: "GET or HEAD required"})
			return
		}
		repos, sizes := r.URL.Query()["repo"], r.URL.Query()["size"]
		if len(repos) != 1 || !badgeValidRepo(repos[0]) || len(sizes) > 1 {
			writeErr(w, &apiError{status: http.StatusBadRequest, code: "bad_repo", msg: "repo must be owner/name"})
			return
		}
		size := "medium"
		if len(sizes) == 1 {
			size = sizes[0]
		}
		if _, _, ok := badgeDimensions(size); !ok {
			writeErr(w, &apiError{status: http.StatusBadRequest, code: "bad_size", msg: "size must be compact, medium or large"})
			return
		}
		if build == nil {
			writeErr(w, &apiError{status: http.StatusInternalServerError, code: "internal", msg: "badge source unavailable"})
			return
		}
		blob, err := build(r.Context(), repos[0])
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) {
				writeErr(w, ae)
			} else {
				writeErr(w, &apiError{status: http.StatusInternalServerError, code: "internal", msg: "badge source unavailable"})
			}
			return
		}
		var c Contract
		if err := json.Unmarshal(blob, &c); err != nil {
			writeErr(w, &apiError{status: http.StatusBadGateway, code: "bad_contract", msg: "invalid cached contract"})
			return
		}
		svg, err := RenderBadgeSVG(repos[0], c, size)
		if err != nil {
			writeErr(w, &apiError{status: http.StatusUnprocessableEntity, code: "bad_contract", msg: "invalid star map contract"})
			return
		}
		sum := sha256.Sum256(svg)
		etag := fmt.Sprintf("\"%x\"", sum[:12])
		w.Header().Set("Cache-Control", "public, max-age=300, stale-while-revalidate=3600")
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'none'; sandbox")
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(svg)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(svg)
	}
}

// handleBadge supports the method-style mux registration used by server.
func (s *server) handleBadge(w http.ResponseWriter, r *http.Request) {
	NewBadgeHandler(s.build)(w, r)
}

func badgeDimensions(size string) (width, height int, ok bool) {
	switch size {
	case "compact":
		return 600, 200, true
	case "medium":
		return 840, 280, true
	case "large":
		return 1080, 360, true
	default:
		return 0, 0, false
	}
}

func badgeValidRepo(repo string) bool {
	if len(repo) > 200 || !repoRe.MatchString(repo) {
		return false
	}
	owner, name, _ := strings.Cut(repo, "/")
	return safePathPart(owner) && safePathPart(name)
}

func badgeHash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// Local seeded random generator, independent of global rand state. It drives
// only the decorative dust/nebula; actual commit stars come from Contract.Main.
type badgeRNG struct{ state uint32 }

func (r *badgeRNG) next() float64 {
	r.state ^= r.state << 13
	r.state ^= r.state >> 17
	r.state ^= r.state << 5
	return float64(r.state) / 4294967296.0
}

type badgeDisc struct {
	arms      int
	phase     float64
	chirality float64
	turns     float64
	ellipse   float64
	tilt      float64
	extent    float64
}

func newBadgeDisc(repo string, turns float64, stars int) badgeDisc {
	r := badgeRNG{state: badgeHash(repo) ^ 0x9e3779b9}
	chirality := 1.0
	if r.next() < 0.5 {
		chirality = -1
	}
	return badgeDisc{
		arms:      2 + int(r.next()*3),
		phase:     r.next() * math.Pi * 2,
		chirality: chirality,
		turns:     clamp(turns, 1.5, 3.2) * (0.8 + r.next()*0.3),
		ellipse:   0.54 + r.next()*0.16,
		tilt:      (r.next() - 0.5) * 0.30,
		extent:    clamp(math.Sqrt(float64(stars)/s0), 0.20, 1),
	}
}

func (d badgeDisc) point(t float64, arm int) badgePoint {
	if t <= 0 {
		return badgePoint{badgeCenterX, badgeCenterY}
	}
	t = clamp(t, 0, 1)
	radius := math.Pow(t, 0.85) * 165 * d.extent
	angle := d.phase + float64(arm)*2*math.Pi/float64(d.arms) + d.chirality*t*d.turns*2*math.Pi
	x := math.Cos(angle) * radius
	y := math.Sin(angle)*radius*d.ellipse + math.Sin(angle*1.9+1.1)*t*5.5
	return badgePoint{
		badgeCenterX + x*math.Cos(d.tilt) - y*math.Sin(d.tilt),
		badgeCenterY + x*math.Sin(d.tilt) + y*math.Cos(d.tilt),
	}
}

// badgeText bounds and XML-escapes *every* untrusted string. XML 1.0 prohibits
// most ASCII controls even when HTML-escaped; strip those as well.
func badgeText(input string, maxRunes int) string {
	var clean strings.Builder
	count := 0
	for _, r := range input {
		if count == maxRunes {
			clean.WriteRune('…')
			break
		}
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0xfffe || r == 0xffff {
			continue
		}
		clean.WriteRune(r)
		count++
	}
	return html.EscapeString(clean.String())
}

// RenderBadgeSVG renders a deterministic, size-bounded, self-contained SVG.
// Repo is validated separately and must match the contract's repository.
// All pixels derive from the same repo seed plus its main/branch history.
func RenderBadgeSVG(repo string, c Contract, size string) ([]byte, error) {
	width, height, ok := badgeDimensions(size)
	if !badgeValidRepo(repo) || !ok || c.Repo != repo || len(c.Main) == 0 || len(c.Main) > maxStars ||
		len(c.Branches) > branchCap || c.CommitTotal < 0 || math.IsNaN(c.Scale.Turns) || math.IsInf(c.Scale.Turns, 0) {
		return nil, errors.New("invalid badge contract or options")
	}
	for _, node := range c.Main {
		if math.IsNaN(node.BirthT) || math.IsInf(node.BirthT, 0) || node.BirthT < 0 || node.BirthT > 1 {
			return nil, errors.New("invalid main node birth time")
		}
	}
	for _, branch := range c.Branches {
		if math.IsNaN(branch.At) || math.IsInf(branch.At, 0) || branch.At < 0 || branch.At > 1 {
			return nil, errors.New("invalid branch time")
		}
	}

	var b strings.Builder
	b.Grow(45000)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" preserveAspectRatio="xMidYMid meet" role="img" aria-labelledby="badgeTitle badgeDesc">`, width, height, badgeViewWidth, badgeViewHeight)
	fmt.Fprintf(&b, "<title id=\"badgeTitle\">%s · Stellar galaxy</title>", badgeText(repo, 200))
	fmt.Fprintf(&b, "<desc id=\"badgeDesc\">Repository %s at %s: %d commits, %d constellations. Genesis at the core; commit history spirals outward.</desc>", badgeText(repo, 200), badgeText(c.HeadSHA, 48), c.CommitTotal, len(c.Branches))
	b.WriteString(`<defs>
<linearGradient id="panel" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#080f23"/><stop offset="0.65" stop-color="#0d1730"/><stop offset="1" stop-color="#061020"/></linearGradient>
<radialGradient id="aura"><stop stop-color="#547ea3" stop-opacity="0.30"/><stop offset="0.35" stop-color="#3b568c" stop-opacity="0.22"/><stop offset="0.77" stop-color="#143453" stop-opacity="0.12"/><stop offset="1" stop-color="#0b1b3c" stop-opacity="0"/></radialGradient>
<radialGradient id="ember"><stop stop-color="#fff1ca" stop-opacity="0.94"/><stop offset="0.13" stop-color="#ffc58b" stop-opacity="0.56"/><stop offset="0.42" stop-color="#e68776" stop-opacity="0.19"/><stop offset="1" stop-color="#dd7570" stop-opacity="0"/></radialGradient>
<linearGradient id="filament" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#ebacc6"/><stop offset="0.33" stop-color="#90dce2"/><stop offset="0.72" stop-color="#8b9bd9"/><stop offset="1" stop-color="#c7a2ce"/></linearGradient>
<linearGradient id="separator" x1="0" y1="0" x2="0" y2="1"><stop stop-color="#8dd1e6" stop-opacity="0"/><stop offset="0.5" stop-color="#8dd1e6" stop-opacity="0.20"/><stop offset="1" stop-color="#8dd1e6" stop-opacity="0"/></linearGradient>
<filter id="soft"><feGaussianBlur stdDeviation="12"/></filter><filter id="softStar"><feGaussianBlur stdDeviation="3.2"/></filter>
<clipPath id="bounds"><rect width="960" height="320" rx="18"/></clipPath>
</defs><g clip-path="url(#bounds)">
<rect width="960" height="320" fill="url(#panel)"/>
`)

	disc := newBadgeDisc(repo, c.Scale.Turns, len(c.Main))
	fmt.Fprintf(&b, `<circle cx="604" cy="160" r="%.1f" fill="url(#aura)"/>`, 24+183*disc.extent)
	fmt.Fprintf(&b, `<ellipse cx="604" cy="160" rx="%.1f" ry="%.1f" transform="rotate(-14 604 160)" fill="#315e86" opacity="0.12" filter="url(#soft)"/>`, 20+188*disc.extent, 12+57*disc.extent)
	fmt.Fprintf(&b, `<ellipse cx="604" cy="160" rx="%.1f" ry="%.1f" transform="rotate(21 604 160)" fill="#6e5e9c" opacity="0.12" filter="url(#soft)"/>`, 16+144*disc.extent, 12+47*disc.extent)
	// Hazy filaments are atmospheric, not additional commits. They follow the
	// same logarithmic winding as the real commit-disc geometry.
	for arm := 0; arm < disc.arms && len(c.Main) > 1; arm++ {
		var path strings.Builder
		for step := 0; step <= 72; step++ {
			t := 0.045 + float64(step)/72*0.955
			p := disc.point(t, arm)
			if step == 0 {
				fmt.Fprintf(&path, "M%.1f %.1f", p.x, p.y)
			} else {
				fmt.Fprintf(&path, "L%.1f %.1f", p.x, p.y)
			}
		}
		fmt.Fprintf(&b, `<path d="%s" stroke="url(#filament)" stroke-width="22" stroke-linecap="round" stroke-opacity="0.25" fill="none" filter="url(#soft)"/>`, path.String())
		fmt.Fprintf(&b, `<path d="%s" stroke="url(#filament)" stroke-width="1.3" stroke-linecap="round" stroke-opacity="0.26" fill="none"/>`, path.String())
	}

	// Disc dust is intentionally much fainter and smaller than commit stars.
	// Its count is capped independently of the history length.
	rng := badgeRNG{state: badgeHash(repo+"/dust") | 1}
	dust := 0
	if len(c.Main) > 1 {
		dust = clampInt(22+int(math.Sqrt(float64(len(c.Main)))*10), 22, 450)
	}
	colors := []string{"#9cdbdf", "#b7bcea", "#dda8ba", "#b2ced9"}
	for i := 0; i < dust; i++ {
		t := math.Pow(rng.next(), 0.80)
		arm := i % disc.arms
		p := disc.point(t, arm)
		spread := 4 + 16*t
		p.x += (rng.next() + rng.next() - 1) * spread
		p.y += (rng.next() + rng.next() - 1) * spread * 0.64
		radius := 0.35 + rng.next()*0.85
		opacity := 0.15 + rng.next()*0.42
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.2f" fill="%s" opacity="%.2f"/>`, p.x, p.y, radius, colors[arm%len(colors)], opacity)
	}
	// A sparse deep-space shell keeps the badge from reading as a flat diagram.
	for i := 0; i < 42; i++ {
		x, y := 322+rng.next()*618, 20+rng.next()*280
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="#b5caec" opacity="%.2f"/>`, x, y, 0.4+rng.next()*0.4, 0.14+rng.next()*0.23)
	}

	// The same index-to-arm relationship as the locked renderer creates
	// multiple solid trunk strands meeting at genesis, not a 2D timeline.
	points := make([]badgePoint, len(c.Main))
	for i := range c.Main {
		t := 0.0
		if len(c.Main) > 1 {
			t = float64(i) / float64(len(c.Main)-1)
		}
		points[i] = disc.point(t, i%disc.arms)
		if i > 0 {
			j := i - disc.arms
			if j < 0 {
				j = 0
			}
			p, q := points[j], points[i]
			fmt.Fprintf(&b, `<path d="M%.1f %.1fL%.1f %.1f" fill="none" stroke="#89bbd2" stroke-opacity="0.37" stroke-width="1.25" stroke-linecap="round"/>`, p.x, p.y, q.x, q.y)
		}
	}

	// Branch archetypes echo the locked hook/arc/kite/wzig/wave sketches.
	// Contract.Branches contains aggregate (K) samples, so the static badge
	// illustrates those summaries without pretending to know missing SHAs.
	shapes := [5][5]badgePoint{
		{{0, 0}, {1.0, 0.55}, {2.1, 0.75}, {3.0, 0.35}, {3.2, -0.45}},
		{{0, 0}, {1.1, 0.6}, {2.3, 0.8}, {3.4, 0.45}, {4.1, -0.2}},
		{{0, 0}, {0.9, 0.8}, {2.0, 1.2}, {3.1, 0.5}, {2.3, -0.5}},
		{{0, 0}, {1.1, 0.75}, {2.0, -0.2}, {3.1, 0.7}, {4.0, -0.15}},
		{{0, 0}, {1.2, 0.45}, {2.3, -0.3}, {3.3, 0.5}, {4.2, 0.05}},
	}
	for i, br := range c.Branches {
		idx := 0
		for j, n := range c.Main {
			if n.BirthT <= br.At {
				idx = j
			}
		}
		base := points[idx]
		shape := shapes[badgeHash(br.N)%uint32(len(shapes))]
		brng := badgeRNG{state: badgeHash(br.N) | 1}
		outx, outy := base.x-badgeCenterX, base.y-badgeCenterY
		mag := math.Hypot(outx, outy)
		if mag < 10 {
			outx, outy, mag = 1, 0, 1
		}
		outx, outy = outx/mag, outy/mag
		turn := 0.5
		if i%2 != 0 {
			turn = -0.5
		}
		dx, dy := outx-turn*outy, outy+turn*outx
		length := math.Hypot(dx, dy)
		dx, dy = dx/length, dy/length
		px, py := -dy, dx
		scale := 12.0 + brng.next()*5
		side := 1.0
		if i%2 != 0 {
			side = -1
		}
		var nodes []badgePoint
		for j := 0; j < clampInt(br.K, 2, 6); j++ {
			// Interpolate along the five-point skeleton at nonuniform steps.
			u := (float64(j) + 0.14*brng.next()) / float64(clampInt(br.K, 2, 6)-1) * 4
			u = clamp(u, 0, 4)
			k := int(math.Min(3, math.Floor(u)))
			f := u - float64(k)
			skel := badgePoint{shape[k].x*(1-f) + shape[k+1].x*f, shape[k].y*(1-f) + shape[k+1].y*f}
			p := badgePoint{
				x: clamp(base.x+dx*(8+skel.x*scale)+px*skel.y*scale*side, 320, 934),
				y: clamp(base.y+dy*(8+skel.x*scale)+py*skel.y*scale*side, 18, 300),
			}
			nodes = append(nodes, p)
		}
		fmt.Fprintf(&b, "<g><title>%s · %s</title>", badgeText(br.N, 90), badgeText(br.Sub, 90))
		for j, p := range nodes {
			from := base
			if j > 0 {
				from = nodes[j-1]
			}
			dash, stroke := "", "#9fc5e2"
			if j == 0 {
				dash = ` stroke-dasharray="2 5"`
			} else {
				stroke = "#b7bce6"
			}
			fmt.Fprintf(&b, `<path d="M%.1f %.1fL%.1f %.1f" stroke="%s" stroke-opacity="0.55" stroke-width="1"%s/>`, from.x, from.y, p.x, p.y, stroke, dash)
			starRadius := 1.8
			if j == len(nodes)-1 {
				starRadius = 3.1 // bright alpha / tip
			}
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="#c1dff2"/>`, p.x, p.y, starRadius)
		}
		if br.Kind == "pr" {
			to := points[clampInt(idx+1, 0, len(points)-1)]
			last := nodes[len(nodes)-1]
			fmt.Fprintf(&b, `<path d="M%.1f %.1fL%.1f %.1f" stroke="#7fa2c2" stroke-opacity="0.35" stroke-width="1" stroke-dasharray="2 5"/>`, last.x, last.y, to.x, to.y)
		}
		b.WriteString("</g>")
	}

	// Four discrete magnitude tiers: normal, merge, release, genesis.
	// Draw stars above lines and nebula. Release/genesis receive diffraction.
	for i, node := range c.Main {
		p := points[i]
		tier := clampInt(node.Tier, 1, 4)
		if i == 0 && node.Type == "genesis" {
			tier = 4
		}
		radii := [4]float64{1.45, 2.25, 3.25, 5.25}
		colors := [4]string{"#dce9ff", "#b6e4eb", "#f1d6bd", "#ffe5bc"}
		radius := radii[tier-1]
		if tier >= 3 {
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="#f9beac" opacity="0.43" filter="url(#softStar)"/>`, p.x, p.y, radius*2.6)
			fmt.Fprintf(&b, `<path d="M%.1f %.1fV%.1fM%.1f %.1fH%.1f" stroke="#fff3d5" stroke-opacity="0.65" stroke-width="0.85"/>`, p.x, p.y-radius*2.9, p.y+radius*2.9, p.x-radius*2.9, p.y, p.x+radius*2.9)
		}
		if i == 0 && node.Type == "genesis" {
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="28" fill="url(#ember)"/>`, p.x, p.y)
			for j := 0; j < 3; j++ {
				a := math.Pi * float64(j) / 3
				dx, dy := math.Cos(a)*15, math.Sin(a)*15
				fmt.Fprintf(&b, `<path d="M%.1f %.1fL%.1f %.1f" stroke="#ffd3a5" stroke-opacity="0.72" stroke-width="0.85"/>`, p.x-dx, p.y-dy, p.x+dx, p.y+dy)
			}
		}
		if node.Type == "release" {
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f" fill="none" stroke="#f5c4b3" stroke-opacity="0.7" stroke-width="0.85"/>`, p.x, p.y, radius+3)
		}
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.2f" fill="%s"><title>%s · %s</title></circle>`, p.x, p.y, radius, colors[tier-1], badgeText(node.SHA, 50), badgeText(node.Label, 64))
	}

	// The information plate is kept away from the galaxy, with type readable
	// even when GitHub scales the SVG down to the width of a README column.
	b.WriteString(`<rect x="0" y="0" width="298" height="320" fill="#0a1225" opacity="0.9"/><path d="M300 26V294" stroke="url(#separator)"/>
<path d="M28 27h13m-13 0v13M931 27h-13m13 0v13M28 293h13m-13 0v-13M931 293h-13m13 0v-13" fill="none" stroke="#9cc9dc" stroke-opacity="0.25" stroke-width="1"/>
<circle cx="39" cy="47" r="4" fill="#a5d6e2"/><text x="52" y="52" font-family="ui-sans-serif,system-ui,Arial,sans-serif" font-size="14" font-weight="700" letter-spacing="2.5" fill="#d8e8f4">STELLAR</text>
<text x="38" y="94" font-family="ui-sans-serif,system-ui,Arial,sans-serif" font-size="10" letter-spacing="2.6" fill="#8da4c6">REPOSITORY GALAXY</text>
`)
	fmt.Fprintf(&b, `<text x="38" y="131" font-family="ui-sans-serif,system-ui,Arial,sans-serif" font-size="21" font-weight="600" fill="#f2f3f4">%s</text>`, badgeText(repo, 19))
	b.WriteString(`<path d="M38 149H258" stroke="#42617d" stroke-opacity="0.48"/>`)
	fmt.Fprintf(&b, `<text x="38" y="185" font-family="ui-sans-serif,system-ui,Arial,sans-serif" font-size="24" font-weight="600" fill="#cce8f4">%d</text>`, c.CommitTotal)
	b.WriteString(`<text x="38" y="203" font-family="ui-sans-serif,system-ui,Arial,sans-serif" font-size="10" letter-spacing="1.2" fill="#8da4c6">COMMITS</text>`)
	fmt.Fprintf(&b, `<text x="172" y="185" font-family="ui-sans-serif,system-ui,Arial,sans-serif" font-size="24" font-weight="600" fill="#cce8f4">%d</text>`, len(c.Branches))
	b.WriteString(`<text x="172" y="203" font-family="ui-sans-serif,system-ui,Arial,sans-serif" font-size="10" letter-spacing="1.2" fill="#8da4c6">CONSTELLATIONS</text>
<circle cx="41" cy="262" r="3.6" fill="#ffd8b1"/><text x="54" y="266" font-family="ui-sans-serif,system-ui,Arial,sans-serif" font-size="11" fill="#a9bcd0">GENESIS</text>
<text x="38" y="286" font-family="ui-monospace,SFMono-Regular,monospace" font-size="10" fill="#829bb6" letter-spacing="0.8">HISTORY → STARS</text>
</g><rect x="0.5" y="0.5" width="959" height="319" rx="17.5" fill="none" stroke="#6080a1" stroke-opacity="0.26"/></svg>`)
	return []byte(b.String()), nil
}
