package main

// A self-contained animated projection for GitHub README ![]() embeds.
// The first frame ignites genesis alone; later frames reveal the real sampled
// commit chain and name-hashed branch constellations. This is intentionally a
// small 2D approximation of the locked Three.js galaxy, not a second layout.

import (
	"bytes"
	"errors"
	"hash/fnv"
	"image"
	"image/color"
	"image/gif"
	"math"
)

const (
	galaxyGIFWidth  = 720
	galaxyGIFHeight = 200
	galaxyGIFFrames = 11
	galaxyGIFBytes  = 1 << 20
)

// Ordered from space-black through blue/teal haze to ice-white starlight.
// The last two warm shades are reserved for the tiny genesis core, not a
// gold/violet overlay. Keeping one small, fixed palette bounds GIF size.
var galaxyGIFPalette = color.Palette{
	color.RGBA{3, 8, 18, 255},
	color.RGBA{5, 14, 28, 255},
	color.RGBA{7, 21, 39, 255},
	color.RGBA{10, 29, 50, 255},
	color.RGBA{13, 39, 61, 255},
	color.RGBA{16, 48, 72, 255},
	color.RGBA{20, 58, 83, 255},
	color.RGBA{25, 73, 98, 255},
	color.RGBA{32, 88, 113, 255},
	color.RGBA{45, 110, 133, 255},
	color.RGBA{66, 131, 155, 255},
	color.RGBA{86, 153, 173, 255},
	color.RGBA{108, 176, 194, 255},
	color.RGBA{143, 203, 218, 255},
	color.RGBA{180, 231, 240, 255},
	color.RGBA{216, 248, 252, 255},
	color.RGBA{249, 255, 255, 255},
	color.RGBA{255, 230, 189, 255},
	color.RGBA{255, 249, 226, 255},
}

type galaxyGIFPoint struct{ x, y float64 }

type galaxyGIFBranch struct {
	anchor galaxyGIFPoint
	stars  []galaxyGIFPoint
	at     float64
	merged bool
}

// renderGalaxyGIF returns a real, endlessly looping GIF89a animation. The
// memory budget is fixed by 720x200 paletted frames (11 x 144 kB), and all
// loops are bounded by the contract's sampled-node and branch ceilings.
// It performs no network I/O and emits no untrusted text or GIF comments.
func renderGalaxyGIF(c Contract) ([]byte, error) {
	if err := validateGalaxyGIFContract(c); err != nil {
		return nil, err
	}

	seed := galaxyGIFHash(c.Repo)
	turns := c.Scale.Turns
	points := make([]galaxyGIFPoint, len(c.Main))
	for i, node := range c.Main {
		if i == 0 {
			points[i] = galaxyGIFPoint{360, 100}
			continue
		}
		// Index preserves ordering when many real commit timestamps coincide;
		// birth time provides additional organic, non-uniform spacing.
		t := 0.73*float64(i)/float64(max(1, len(c.Main)-1)) + 0.27*node.BirthT
		p := galaxyGIFDisc(t, 0, seed, turns)
		h := galaxyGIFHash(node.SHA)
		p.x += (float64(h&255)/255 - 0.5) * (3 + 5*t)
		p.y += (float64((h>>8)&255)/255 - 0.5) * (2 + 4*t)
		points[i] = p
	}

	branches := make([]galaxyGIFBranch, len(c.Branches))
	for i, branch := range c.Branches {
		branches[i] = galaxyGIFConstellation(branch, i, len(c.Branches), points, c.Main, seed)
	}

	animation := &gif.GIF{
		Image:     make([]*image.Paletted, 0, galaxyGIFFrames),
		Delay:     make([]int, 0, galaxyGIFFrames),
		LoopCount: 0,
	}
	for frame := 0; frame < galaxyGIFFrames; frame++ {
		p := image.NewPaletted(image.Rect(0, 0, galaxyGIFWidth, galaxyGIFHeight), galaxyGIFPalette)
		galaxyGIFBackground(p)
		progress := math.Pow(float64(frame)/float64(galaxyGIFFrames-1), 1.06)
		if frame > 0 {
			galaxyGIFNebula(p, seed, turns, progress, frame)
			galaxyGIFSpaceStars(p, seed, frame)
			galaxyGIFHistory(p, c.Main, points, progress, frame)
			for _, b := range branches {
				galaxyGIFDrawBranch(p, b, progress, frame)
			}
		}
		// Always draw genesis last, giving it priority over dust and lines.
		galaxyGIFGenesis(p, frame)
		animation.Image = append(animation.Image, p)
		switch frame {
		case 0:
			animation.Delay = append(animation.Delay, 35) // standalone ignition
		case galaxyGIFFrames - 1:
			animation.Delay = append(animation.Delay, 65) // hold completed galaxy
		default:
			animation.Delay = append(animation.Delay, 13)
		}
	}

	var output bytes.Buffer
	if err := gif.EncodeAll(&output, animation); err != nil {
		return nil, err
	}
	if output.Len() >= galaxyGIFBytes {
		return nil, errors.New("galaxy GIF exceeds 1 MiB")
	}
	return output.Bytes(), nil
}

func validateGalaxyGIFContract(c Contract) error {
	if len(c.Repo) == 0 || len(c.Repo) > 256 || len(c.Main) == 0 || len(c.Main) > 1400 ||
		len(c.Branches) > 8 || c.Main[0].Type != "genesis" || c.CommitTotal < 0 ||
		math.IsNaN(c.Scale.Turns) || math.IsInf(c.Scale.Turns, 0) || c.Scale.Turns < 0 || c.Scale.Turns > 6 {
		return errors.New("invalid galaxy GIF contract")
	}
	previous := 0.0
	for i, n := range c.Main {
		if len(n.SHA) > 256 || math.IsNaN(n.BirthT) || math.IsInf(n.BirthT, 0) ||
			n.BirthT < previous || n.BirthT > 1 || (i == 0 && n.BirthT != 0) {
			return errors.New("invalid galaxy GIF main node")
		}
		previous = n.BirthT
	}
	for _, b := range c.Branches {
		if len(b.N) > 256 || math.IsNaN(b.At) || math.IsInf(b.At, 0) || b.At < 0 || b.At > 1 {
			return errors.New("invalid galaxy GIF branch")
		}
	}
	return nil
}

func galaxyGIFHash(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return h.Sum32()
}

func galaxyGIFMix(x uint32) uint32 {
	x ^= x >> 16
	x *= 0x7feb352d
	x ^= x >> 15
	x *= 0x846ca68b
	return x ^ (x >> 16)
}

func galaxyGIFDisc(t float64, arm int, seed uint32, turns float64) galaxyGIFPoint {
	t = math.Max(0, math.Min(1, t))
	if t == 0 {
		return galaxyGIFPoint{360, 100}
	}
	if turns == 0 {
		turns = 2.15
	}
	phase := float64(seed%360) * math.Pi / 180
	chirality := 1.0
	if seed&1 != 0 {
		chirality = -1
	}
	angle := phase + float64(arm)*2*math.Pi/3 + chirality*t*turns*2*math.Pi
	radius := 218 * math.Pow(t, 0.84)
	// A tilted, domain-warped disc: X and Y have different projection scales,
	// and the ripples vary as history grows outwards from (360,100).
	x := radius*math.Cos(angle) + 6*t*math.Sin(2.7*angle+5*t)
	y := radius*0.31*math.Sin(angle) + 6*t*math.Sin(1.8*angle-3*t)
	return galaxyGIFPoint{360 + x - y*0.13, 100 + y + x*0.095}
}

func galaxyGIFConstellation(b BranchNode, index, total int, main []galaxyGIFPoint, nodes []MainNode, seed uint32) galaxyGIFBranch {
	h := galaxyGIFHash(b.N)
	anchor := main[0]
	for i, n := range nodes {
		if n.BirthT > b.At {
			break
		}
		anchor = main[i]
	}
	// Name chooses both one of five asterisms and its stable position; the
	// branch count only distributes the base azimuths to avoid exact overlap.
	angle := (float64(index) + 0.20 + float64(h%83)/150) / float64(max(1, total)) * 2 * math.Pi
	outer := galaxyGIFPoint{
		x: 360 + 235*math.Cos(angle),
		y: 100 + 68*math.Sin(angle),
	}
	count := max(2, min(6, b.K))
	stars := make([]galaxyGIFPoint, count)
	rotation := angle + math.Pi/2 + (float64((h>>8)%100)/100-0.5)*0.65
	for i := range stars {
		u := float64(i+1) / float64(count)
		spacing := u + 0.10*math.Sin(float64(i)*2.7+float64(h%17))
		dx, dy := 0.0, 0.0
		switch h % 5 {
		case 0: // hook
			dx, dy = 33*spacing, 19*math.Sin(math.Pi*u)
		case 1: // arc
			dx, dy = 35*spacing, -20*math.Sin(math.Pi*u)
		case 2: // kite
			dx, dy = 25*spacing, 14*math.Sin(u*2*math.Pi)
		case 3: // zigzag
			dx, dy = 33*spacing, 13*math.Sin(float64(i)*math.Pi+math.Pi/2)
		default: // wave
			dx, dy = 34*spacing, 12*math.Sin(u*3*math.Pi)
		}
		x := outer.x + dx*math.Cos(rotation) - dy*math.Sin(rotation)
		y := outer.y + dx*math.Sin(rotation) + dy*math.Cos(rotation)
		stars[i] = galaxyGIFPoint{math.Max(8, math.Min(712, x)), math.Max(9, math.Min(191, y))}
	}
	return galaxyGIFBranch{anchor: anchor, stars: stars, at: b.At, merged: b.Kind == "pr"}
}

func galaxyGIFBackground(p *image.Paletted) {
	for y := 0; y < galaxyGIFHeight; y++ {
		for x := 0; x < galaxyGIFWidth; x++ {
			dx := float64(x-360) / 380
			dy := float64(y-100) / 108
			v := dx*dx + dy*dy
			shade := uint8(0)
			if v < 1.2 {
				shade = 1
			}
			if v < 0.48 {
				shade = 2
			}
			p.Pix[y*p.Stride+x] = shade
		}
	}
}

func galaxyGIFPut(p *image.Paletted, x, y int, shade uint8) {
	if x >= 0 && x < galaxyGIFWidth && y >= 0 && y < galaxyGIFHeight {
		off := y*p.Stride + x
		if p.Pix[off] < shade {
			p.Pix[off] = shade
		}
	}
}

func galaxyGIFGlow(p *image.Paletted, x, y, radius int, shade uint8) {
	for yy := -radius; yy <= radius; yy++ {
		for xx := -radius; xx <= radius; xx++ {
			d2 := xx*xx + yy*yy
			if d2 > radius*radius {
				continue
			}
			level := shade
			if d2*4 > radius*radius {
				level = max(3, shade-4)
			} else if d2*2 > radius*radius {
				level = max(4, shade-2)
			}
			galaxyGIFPut(p, x+xx, y+yy, level)
		}
	}
}

func galaxyGIFLine(p *image.Paletted, a, b galaxyGIFPoint, shade uint8, dotted bool, phase int) {
	steps := int(math.Ceil(math.Max(math.Abs(b.x-a.x), math.Abs(b.y-a.y))))
	if steps == 0 {
		return
	}
	for i := 0; i <= steps; i++ {
		if dotted && (i+phase)%8 >= 3 {
			continue
		}
		t := float64(i) / float64(steps)
		x := int(math.Round(a.x + (b.x-a.x)*t))
		y := int(math.Round(a.y + (b.y-a.y)*t))
		galaxyGIFPut(p, x, y, shade)
	}
}

func galaxyGIFNebula(p *image.Paletted, seed uint32, turns, progress float64, frame int) {
	// Overlapping thin filaments, irregular widths and hashed gaps approximate
	// domain-warped gas at three apparent depths, without per-pixel noise or
	// expensive blur passes. Frame drift supplies parallax-like motion.
	for arm := 0; arm < 3; arm++ {
		for step := 1; step <= 190; step++ {
			t := float64(step) / 190
			if t > progress {
				break
			}
			p0 := galaxyGIFDisc(t, arm, seed, turns)
			h := galaxyGIFMix(seed + uint32(arm*701+step*17))
			wind := math.Sin(t*34+float64(arm)*3+float64(frame)*0.12) * (3 + 5*t)
			p0.x += wind
			p0.y += math.Cos(t*24+float64(arm)*2+float64(frame)*0.07) * 3
			if h%7 == 0 {
				continue
			}
			r := 3 + int(h%5) + int(5*t)
			galaxyGIFGlow(p, int(p0.x), int(p0.y), r, 5+uint8((h>>8)%3))
			if h%4 == 0 {
				galaxyGIFPut(p, int(p0.x), int(p0.y), 10)
			}
		}
	}
}

func galaxyGIFSpaceStars(p *image.Paletted, seed uint32, frame int) {
	for i := uint32(0); i < 165; i++ {
		h := galaxyGIFMix(seed ^ (i+1)*0x9e3779b9)
		x, y := int(h%720), int((h>>12)%200)
		shade := uint8(8)
		if int(h%9) == frame%9 {
			shade = 12
		}
		galaxyGIFPut(p, x, y, shade)
	}
}

func galaxyGIFStar(p *image.Paletted, point galaxyGIFPoint, tier int, frame int) {
	tier = max(1, min(4, tier))
	x, y := int(math.Round(point.x)), int(math.Round(point.y))
	galaxyGIFGlow(p, x, y, tier+1, uint8(11+tier))
	if tier >= 3 {
		for d := -tier - 3; d <= tier+3; d++ {
			shade := uint8(10)
			if d > -2 && d < 2 {
				shade = 14
			}
			galaxyGIFPut(p, x+d, y, shade)
			galaxyGIFPut(p, x, y+d, shade)
		}
	}
	galaxyGIFPut(p, x, y, 16)
	if tier >= 2 && frame%4 == 0 {
		galaxyGIFPut(p, x+1, y, 15)
	}
}

func galaxyGIFGenesis(p *image.Paletted, frame int) {
	r := 5 + (frame % 4)
	galaxyGIFGlow(p, 360, 100, r+4, 11)
	galaxyGIFGlow(p, 360, 100, r, 16)
	for d := -13; d <= 13; d++ {
		shade := uint8(11)
		if d >= -5 && d <= 5 {
			shade = 15
		}
		galaxyGIFPut(p, 360+d, 100, shade)
		galaxyGIFPut(p, 360, 100+d, shade)
		if d%2 == 0 && d >= -9 && d <= 9 {
			galaxyGIFPut(p, 360+d, 100+d/2, 12)
			galaxyGIFPut(p, 360+d, 100-d/2, 12)
		}
	}
	galaxyGIFPut(p, 360, 100, 18)
}

func galaxyGIFHistory(p *image.Paletted, nodes []MainNode, points []galaxyGIFPoint, progress float64, frame int) {
	for i := 1; i < len(nodes); i++ {
		if nodes[i].BirthT > progress {
			continue
		}
		galaxyGIFLine(p, points[i-1], points[i], 11, false, 0)
	}
	// Pulsing motes follow actual parent -> child trunk segments.
	if len(nodes) > 1 {
		for i := 0; i < 100; i++ {
			v := math.Mod(float64(i)*0.61803398875+float64(frame)*0.037, 1)
			j := min(len(nodes)-2, int(v*float64(len(nodes)-1)))
			if nodes[j+1].BirthT > progress {
				continue
			}
			local := v*float64(len(nodes)-1) - float64(j)
			x := points[j].x + (points[j+1].x-points[j].x)*local
			y := points[j].y + (points[j+1].y-points[j].y)*local
			galaxyGIFPut(p, int(x), int(y), 14)
		}
	}
	for i := 1; i < len(nodes); i++ {
		if nodes[i].BirthT <= progress {
			galaxyGIFStar(p, points[i], nodes[i].Tier, frame)
		}
	}
}

func galaxyGIFDrawBranch(p *image.Paletted, b galaxyGIFBranch, progress float64, frame int) {
	if progress < b.at || len(b.stars) == 0 {
		return
	}
	// The whole true chain is visible by the final frame, even for a branch
	// formed close to HEAD. Partial frames grow it star by star.
	fraction := 1.0
	if progress < 1 {
		fraction = math.Min(1, (progress-b.at)/math.Max(0.08, 1-b.at))
	}
	count := min(len(b.stars), max(1, int(math.Ceil(fraction*float64(len(b.stars))))))
	galaxyGIFLine(p, b.anchor, b.stars[0], 10, true, frame)
	for i := 1; i < count; i++ {
		galaxyGIFLine(p, b.stars[i-1], b.stars[i], 12, false, 0)
	}
	if b.merged && count == len(b.stars) {
		galaxyGIFLine(p, b.stars[count-1], b.anchor, 8, true, frame+4)
	}
	for i := 0; i < count; i++ {
		galaxyGIFStar(p, b.stars[i], 2+(i+frame/3)%2, frame)
	}
}
