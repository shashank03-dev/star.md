package main

import (
	"bytes"
	"image/gif"
	"math"
	"testing"
)

func galaxyGIFFixture() Contract {
	return Contract{
		Repo: "acme/stellar", HeadSHA: "1234567", CommitTotal: 130,
		Scale: Scale{Stars: 7, Span: 26, Turns: 2.15, Duration: 14},
		Main: []MainNode{
			{Type: "genesis", Tier: 4, SHA: "0000000", BirthT: 0},
			{Type: "commit", Tier: 1, SHA: "1111111", BirthT: 0.12},
			{Type: "commit", Tier: 2, SHA: "2222222", BirthT: 0.25},
			{Type: "release", Tier: 3, SHA: "3333333", BirthT: 0.44},
			{Type: "commit", Tier: 1, SHA: "4444444", BirthT: 0.60},
			{Type: "commit", Tier: 1, SHA: "5555555", BirthT: 0.80},
			{Type: "commit", Tier: 2, SHA: "6666666", BirthT: 1},
		},
		Branches: []BranchNode{
			{N: "feature/nebula", At: 0.30, K: 5, Kind: "ref"},
			{N: "fix/halo", At: 0.68, K: 3, Kind: "pr"},
		},
	}
}

func TestRenderGalaxyGIFAnimatedDecodableAndSmall(t *testing.T) {
	data, err := renderGalaxyGIF(galaxyGIFFixture())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("GIF89a")) {
		t.Fatal("expected GIF89a data")
	}
	if len(data) >= 1<<20 {
		t.Fatalf("GIF too large: %d bytes", len(data))
	}
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("cannot decode animated GIF: %v", err)
	}
	if g.Config.Width != 720 || g.Config.Height != 200 {
		t.Fatalf("unexpected dimensions: %dx%d", g.Config.Width, g.Config.Height)
	}
	if len(g.Image) < 8 || len(g.Image) > 12 || len(g.Delay) != len(g.Image) {
		t.Fatalf("unexpected frame count: frames=%d delays=%d", len(g.Image), len(g.Delay))
	}
	if g.LoopCount != 0 {
		t.Fatalf("expected infinite loop, got %d", g.LoopCount)
	}
	for i, frame := range g.Image {
		if frame.Bounds() != g.Image[0].Bounds() || g.Delay[i] <= 0 {
			t.Fatalf("invalid frame %d bounds or delay", i)
		}
	}
	// The first frame is a true genesis-only ignition, not a fully drawn
	// still image paired with multiple animation frames.
	first, last := g.Image[0], g.Image[len(g.Image)-1]
	if first.ColorIndexAt(360, 100) < 14 {
		t.Fatal("genesis star missing from first frame")
	}
	differences, changedFrames := 0, 0
	for i := 1; i < len(g.Image); i++ {
		delta := 0
		for p, color := range g.Image[i].Pix {
			if color != g.Image[i-1].Pix[p] {
				delta++
			}
		}
		if delta > 0 {
			changedFrames++
		}
	}
	for p, color := range first.Pix {
		if color != last.Pix[p] {
			differences++
		}
	}
	if changedFrames < len(g.Image)-2 || differences < 250 {
		t.Fatalf("frames appear static: %d changing transitions, %d changed pixels", changedFrames, differences)
	}
}

func TestRenderGalaxyGIFDeterministicAndContractDriven(t *testing.T) {
	c := galaxyGIFFixture()
	a, err := renderGalaxyGIF(c)
	if err != nil {
		t.Fatal(err)
	}
	b, err := renderGalaxyGIF(c)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("same input rendered differently: %v", err)
	}
	c.Main[4].SHA = "different-sha"
	changed, err := renderGalaxyGIF(c)
	if err != nil || bytes.Equal(a, changed) {
		t.Fatalf("changing a commit did not affect GIF: %v", err)
	}
	c = galaxyGIFFixture()
	c.Branches[0].N = "some-other-constellation"
	changed, err = renderGalaxyGIF(c)
	if err != nil || bytes.Equal(a, changed) {
		t.Fatalf("changing a branch did not affect GIF: %v", err)
	}
	c = galaxyGIFFixture()
	c.Branches[0].N = `<script>alert("x")</script>`
	c.Main[3].Label = `<unsafe&title>`
	changed, err = renderGalaxyGIF(c)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(changed, []byte("<script")) || bytes.Contains(changed, []byte("<unsafe")) {
		t.Fatal("untrusted contract text embedded in GIF")
	}
}

func TestRenderGalaxyGIFBoundsAndInvalidContracts(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Contract)
	}{
		{"missing history", func(c *Contract) { c.Main = nil }},
		{"too many commits", func(c *Contract) { c.Main = make([]MainNode, 1401) }},
		{"too many branches", func(c *Contract) { c.Branches = make([]BranchNode, 9) }},
		{"non-genesis start", func(c *Contract) { c.Main[0].Type = "commit" }},
		{"invalid birth", func(c *Contract) { c.Main[1].BirthT = math.NaN() }},
		{"negative birth", func(c *Contract) { c.Main[1].BirthT = -1 }},
		{"out-of-order birth", func(c *Contract) { c.Main[2].BirthT = 0.01 }},
		{"invalid attachment", func(c *Contract) { c.Branches[0].At = math.Inf(1) }},
		{"invalid turns", func(c *Contract) { c.Scale.Turns = math.NaN() }},
		{"oversized repo", func(c *Contract) { c.Repo = string(bytes.Repeat([]byte{'x'}, 300)) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := galaxyGIFFixture()
			tc.edit(&c)
			if _, err := renderGalaxyGIF(c); err == nil {
				t.Fatal("expected invalid contract error")
			}
		})
	}
	// Maximum supported input still respects the output size and dimensions.
	c := galaxyGIFFixture()
	c.Main = make([]MainNode, 1400)
	for i := range c.Main {
		c.Main[i] = MainNode{Type: "commit", Tier: (i%4 + 1), SHA: string([]byte{byte(i), byte(i >> 8)}), BirthT: float64(i) / 1399}
	}
	c.Main[0].Type = "genesis"
	c.Branches = make([]BranchNode, 8)
	for i := range c.Branches {
		c.Branches[i] = BranchNode{N: string(rune('a' + i)), At: float64(i+1) / 10, K: 6}
	}
	data, err := renderGalaxyGIF(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) >= 1<<20 {
		t.Fatalf("max-size input generated %d-byte GIF", len(data))
	}
	if _, err := gif.DecodeAll(bytes.NewReader(data)); err != nil {
		t.Fatalf("invalid maximum-input GIF: %v", err)
	}
}
