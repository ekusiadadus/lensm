package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"regexp"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"loov.dev/lensm/internal/f32color"
)

var (
	HeaderHeightRem  = unit.Sp(1.5)
	HeaderBackground = f32color.NRGBAHex(0xAAAAAAFF)

	SecondaryBackground = color.NRGBA{R: 0xF0, G: 0xF0, B: 0xF0, A: 0xFF}
	SplitterBackground  = color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xFF}
)

const N = 44

func Fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	return Fibonacci(n-1) + Fibonacci(n-2)
}

var _ = Fibonacci(0)

func main() {
	text := flag.Bool("text", false, "show text output")
	filter := flag.String("filter", "", "filter the symbol by regexp")
	context := flag.Int("context", 3, "source line context")
	maxMatches := flag.Int("max-matches", 10, "maximum number of matches to parse")
	flag.Parse()
	exename := flag.Arg(0)

	if exename == "" || *filter == "" {
		fmt.Fprintln(os.Stderr, "lensm -filter main <exename>")
		flag.Usage()
		os.Exit(1)
	}

	re, err := regexp.Compile(*filter)
	if err != nil {
		panic(err)
	}

	out, err := Parse(Options{
		Exe:        exename,
		Filter:     re,
		Context:    *context,
		MaxSymbols: *maxMatches,
	})
	if err != nil {
		panic(err)
	}

	if *text {
		for _, symbol := range out.Matches {
			fmt.Printf("\n\n// func %v (%v)\n", symbol.Name, symbol.File)
			for _, ix := range symbol.Code {
				if ix.RefPC != 0 {
					fmt.Printf("    %-60v %v@%3v %08x --> %08x\n", ix.Text, ix.File, ix.Line, ix.PC, ix.RefPC)
				} else {
					fmt.Printf("    %-60v %v@%3v %08x\n", ix.Text, ix.File, ix.Line, ix.PC)
				}
			}

			fmt.Printf("// CONTEXT\n")
			for _, source := range symbol.Source {
				fmt.Printf("// FILE  %v\n", source.File)
				for i, block := range source.Blocks {
					if i > 0 {
						fmt.Printf("...:\n")
					}
					for line, text := range block.Lines {
						fmt.Printf("%3d:  %v\n", block.From+line, text)
					}
				}
			}
		}
		fmt.Println("MORE", out.More)
		os.Exit(0)
	}

	ui := NewUI()
	ui.Output = out

	// This creates a new application window and starts the UI.
	go func() {
		w := app.NewWindow(
			app.Title("lensm"),
			app.Size(unit.Dp(1400), unit.Dp(900)),
		)
		if err := ui.Run(w); err != nil {
			log.Println(err)
			os.Exit(1)
		}
		os.Exit(0)
	}()

	// This starts Gio main.
	app.Main()
}

type UI struct {
	Theme *material.Theme

	AutoRefresh widget.Bool

	Filter   widget.Editor
	Output   *Output
	Matches  widget.List
	Selected *Match
	MatchUI  MatchUIState
}

func NewUI() *UI {
	ui := &UI{}
	ui.Theme = material.NewTheme(gofont.Collection())
	ui.Matches.List.Axis = layout.Vertical

	ui.Filter.SetText("gioui.org.*decode")
	ui.Filter.SingleLine = true
	return ui
}

func (ui *UI) Run(w *app.Window) error {
	var ops op.Ops
	for {
		select {
		case e := <-w.Events():
			switch e := e.(type) {
			case system.FrameEvent:
				gtx := layout.NewContext(&ops, e)
				ui.Layout(gtx)
				e.Frame(gtx.Ops)

			case system.DestroyEvent:
				return e.Err
			}
		}
	}
}

func (ui *UI) Layout(gtx layout.Context) {
	if ui.Selected == nil && len(ui.Output.Matches) > 0 {
		ui.selectMatch(&ui.Output.Matches[0])
	}

	layout.Flex{
		Axis: layout.Vertical,
	}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{
				Axis:      layout.Horizontal,
				Alignment: layout.Middle,
			}.Layout(gtx,
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Flexed(1,
					func(gtx layout.Context) layout.Dimensions {
						return widget.Border{
							Color: ui.Theme.Fg,
							Width: 1,
						}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.UniformInset(2).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								editor := material.Editor(ui.Theme, &ui.Filter, "Filter")
								return editor.Layout(gtx)
							})
						})
					}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					_, err := regexp.Compile(ui.Filter.Text())
					if err != nil {
						body1 := material.Body1(ui.Theme, err.Error())
						body1.Color = color.NRGBA{R: 0xC0, A: 0xFF}
						body1.MaxLines = 1
						return body1.Layout(gtx)
					}
					body1 := material.Body1(ui.Theme, "ok")
					body1.MaxLines = 1
					return body1.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
				layout.Rigid(
					material.CheckBox(ui.Theme, &ui.AutoRefresh, "Auto Refresh").Layout,
				),
				layout.Rigid(layout.Spacer{Width: 8}.Layout),
			)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{
				Axis: layout.Horizontal,
			}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					size := gtx.Constraints.Max
					gtx.Constraints = layout.Exact(image.Point{
						X: gtx.Metric.Sp(10 * 20),
						Y: gtx.Constraints.Max.Y,
					})
					paint.FillShape(gtx.Ops, SecondaryBackground, clip.Rect{Max: size}.Op())
					return ui.layoutMatches(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					size := image.Point{
						X: gtx.Metric.Dp(1),
						Y: gtx.Constraints.Max.Y,
					}
					paint.FillShape(gtx.Ops, SplitterBackground, clip.Rect{Max: size}.Op())
					return layout.Dimensions{
						Size: size,
					}
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(gtx.Constraints.Max)
					if ui.Selected == nil {
						return material.H4(ui.Theme, "nothing selected").Layout(gtx)
					}
					return ui.layoutCode(gtx, ui.Selected)
				}),
			)
		}),
	)
}

type ListBox struct {
	focused bool
	active  int
	scroll  widget.List
}

func (ui *UI) layoutMatches(gtx layout.Context) layout.Dimensions {
	defer clip.Rect{Max: gtx.Constraints.Min}.Push(gtx.Ops).Pop()

	key.InputOp{
		Tag:  123,
		Keys: key.NameUpArrow + "|" + key.NameDownArrow,
	}.Add(gtx.Ops)

	n := len(ui.Output.Matches)
	if ui.Output.More {
		n += 1
	}

	for i := range ui.Output.Matches {
		match := &ui.Output.Matches[i]
		for match.Select.Clicked() {
			ui.selectMatch(match)
		}
	}

	var focusOffset int
	for _, ev := range gtx.Events(123) {
		fmt.Printf("%#v\n", ev)
		if ev, ok := ev.(key.Event); ok {
			if ev.State != key.Press {
				continue
			}
			switch ev.Name {
			case key.NameUpArrow:
				focusOffset--
			case key.NameDownArrow:
				focusOffset++
			}
		}
	}
	if focusOffset != 0 {
		fmt.Println("focus offset changed")
	}

	return material.List(ui.Theme, &ui.Matches).Layout(gtx, n,
		func(gtx layout.Context, index int) layout.Dimensions {
			if index >= len(ui.Output.Matches) {
				return material.Body2(ui.Theme, "... too many matches ...").Layout(gtx)
			}
			return ui.layoutMatch(gtx, &ui.Output.Matches[index])
		})
}

func (ui *UI) layoutMatch(gtx layout.Context, match *Match) layout.Dimensions {
	return material.Clickable(gtx, &match.Select, func(gtx layout.Context) layout.Dimensions {
		style := material.Body2(ui.Theme, match.Name)
		style.MaxLines = 1
		style.TextSize = unit.Sp(10)
		if match == ui.Selected {
			style.Font.Weight = text.Heavy
		}
		tgtx := gtx
		tgtx.Constraints.Max.X = 100000
		dims := style.Layout(tgtx) // layout.UniformInset(unit.Dp(8)).Layout(gtx, style.Layout)
		return layout.Dimensions{
			Size: image.Point{
				X: gtx.Constraints.Max.X,
				Y: dims.Size.Y,
			},
		}
	})
}

func (ui *UI) selectMatch(target *Match) {
	if ui.Selected == target {
		return
	}
	ui.Selected = target
	ui.MatchUI.asm.scroll = 100000
	ui.MatchUI.src.scroll = 100000
}

func (ui *UI) layoutCode(gtx layout.Context, match *Match) layout.Dimensions {
	return MatchUIStyle{
		Theme:        ui.Theme,
		Match:        ui.Selected,
		MatchUIState: &ui.MatchUI,
		TextHeight:   unit.Sp(12),
		LineHeight:   unit.Sp(14),
	}.Layout(gtx)
}
