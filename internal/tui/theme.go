package tui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"

	"github.com/yyZe0122/yunmengze-agent/internal/platform/paths"
)

type ThemeName string

const (
	ThemeDay   ThemeName = "day"
	ThemeNight ThemeName = "night"

	tuiPrefsFilename = "tui.json"
	defaultTheme     = ThemeNight

	hexNightPaper = "#0C0C0B"
	hexNightInk   = "#1A1916"
	hexNightWash  = "#2A2824"
	hexNightHair  = "#4A4740"
	hexNightBone  = "#C8C2B4"
	hexNightFly   = "#7A756C"
	hexNightSeal  = "#C73E3A"
	hexNightStamp = "#8A1F1A"
	hexNightMix   = "#E8DCC8"
	hexPlanOchre  = "#A67C52"

	hexDayPaper = "#EDE6D6"
	hexDayInk   = "#F7F1E4"
	hexDayWash  = "#D9D0BE"
	hexDayHair  = "#8A8374"
	hexDayBone  = "#1A1916"
	hexDaySeal  = "#9B2D28"
	hexDayStamp = "#6E1A16"
)

// Theme is the 焦墨 pixel palette (night xuan / day xuan-paper).
type Theme struct {
	Name ThemeName

	Paper color.Color
	Ink   color.Color
	Wash  color.Color
	Hair  color.Color
	Bone  color.Color
	Fly   color.Color
	Seal  color.Color
	Stamp color.Color
	Mix   color.Color

	ModeAgent color.Color
	ModePlan  color.Color
	ModeAuto  color.Color
}

// Day: 宣纸昼 — warm paper, cinnabar seal.
var dayTheme = Theme{
	Name:      ThemeDay,
	Paper:     lipgloss.Color(hexDayPaper),
	Ink:       lipgloss.Color(hexDayInk),
	Wash:      lipgloss.Color(hexDayWash),
	Hair:      lipgloss.Color(hexDayHair),
	Bone:      lipgloss.Color(hexDayBone),
	Fly:       lipgloss.Color(hexDayHair),
	Seal:      lipgloss.Color(hexDaySeal),
	Stamp:     lipgloss.Color(hexDayStamp),
	Mix:       lipgloss.Color(hexDayBone),
	ModeAgent: lipgloss.Color(hexDayBone),
	ModePlan:  lipgloss.Color(hexPlanOchre),
	ModeAuto:  lipgloss.Color(hexDaySeal),
}

// Night: 焦墨夜 — xuan paper, cinnabar seal.
var nightTheme = Theme{
	Name:      ThemeNight,
	Paper:     lipgloss.Color(hexNightPaper),
	Ink:       lipgloss.Color(hexNightInk),
	Wash:      lipgloss.Color(hexNightWash),
	Hair:      lipgloss.Color(hexNightHair),
	Bone:      lipgloss.Color(hexNightBone),
	Fly:       lipgloss.Color(hexNightFly),
	Seal:      lipgloss.Color(hexNightSeal),
	Stamp:     lipgloss.Color(hexNightStamp),
	Mix:       lipgloss.Color(hexNightMix),
	ModeAgent: lipgloss.Color(hexNightBone),
	ModePlan:  lipgloss.Color(hexPlanOchre),
	ModeAuto:  lipgloss.Color(hexNightSeal),
}

type tuiPrefs struct {
	Theme ThemeName `json:"theme"`
}

func themeByName(name ThemeName) Theme {
	switch name {
	case ThemeDay:
		return dayTheme
	case ThemeNight:
		return nightTheme
	default:
		return nightTheme
	}
}

func toggleTheme(name ThemeName) ThemeName {
	if name == ThemeDay {
		return ThemeNight
	}
	return ThemeDay
}

func applyTheme(t Theme) {
	colorPaper = t.Paper
	colorInk = t.Ink
	colorWash = t.Wash
	colorHair = t.Hair
	colorBone = t.Bone
	colorFly = t.Fly
	colorSeal = t.Seal
	colorMix = t.Mix

	colorDim = t.Fly
	colorOK = t.Bone
	colorWarn = t.ModePlan
	colorErr = t.Seal
	colorMuted = t.Fly
	colorBorder = t.Hair
	colorHeart = t.Seal
	colorTitle = t.Mix
	colorInput = t.Bone
	colorSurface = t.Wash
	colorModeAgent = t.ModeAgent
	colorModePlan = t.ModePlan
	colorModeAuto = t.ModeAuto
	colorBubbleUser = t.Seal
	colorBubbleAssistant = t.Ink
	colorBubbleThinking = t.Hair
	colorBubbleTool = t.Wash
	colorKeyword = t.Seal

	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(colorTitle)
	styleDim = lipgloss.NewStyle().Foreground(colorDim)
	styleMuted = lipgloss.NewStyle().Foreground(colorMuted)
	styleError = lipgloss.NewStyle().Foreground(colorErr)
	styleOK = lipgloss.NewStyle().Foreground(colorOK)
	styleWarn = lipgloss.NewStyle().Foreground(colorWarn)
	styleBadge = lipgloss.NewStyle().Foreground(colorSeal).Bold(true)
	styleStatus = lipgloss.NewStyle().Foreground(colorHair)
	styleInput = lipgloss.NewStyle().Foreground(colorInput).Background(colorInk)
	styleKeyword = lipgloss.NewStyle().Foreground(colorKeyword).Bold(true)
	styleCompSel = lipgloss.NewStyle().Foreground(colorSeal).Bold(true)
	styleComp = lipgloss.NewStyle().Foreground(colorDim)
	styleHelpBox = inkPanel(colorWash, colorHair, 0).Padding(0, 1)
	styleRiskHi = lipgloss.NewStyle().Foreground(colorErr).Bold(true)
	styleRiskMed = lipgloss.NewStyle().Foreground(colorWarn)
	styleRiskLo = lipgloss.NewStyle().Foreground(colorOK)
	styleTLUser = lipgloss.NewStyle().Foreground(colorSeal).Bold(true)
	styleTLSys = lipgloss.NewStyle().Foreground(colorDim)
	styleTLPlan = lipgloss.NewStyle().Foreground(colorModePlan)
	styleTLRun = lipgloss.NewStyle().Foreground(colorBone)
	styleTLErr = lipgloss.NewStyle().Foreground(colorErr)
	styleTLTool = lipgloss.NewStyle().Foreground(colorBone)
	styleTLThinking = lipgloss.NewStyle().Foreground(colorBone)
	styleTLReply = lipgloss.NewStyle().Foreground(colorBone)
	styleTLBody = lipgloss.NewStyle().Foreground(colorDim)
	styleTLJourney = lipgloss.NewStyle().Foreground(colorFly)
	styleDone = lipgloss.NewStyle().Foreground(colorSeal).Bold(true)
	styleHeart = lipgloss.NewStyle().Foreground(colorHeart)
	styleMetricsTitle = lipgloss.NewStyle().Bold(true).Foreground(colorMix)
	stylePanelLabel = lipgloss.NewStyle().Foreground(colorFly)
	styleModeAgent = lipgloss.NewStyle().Foreground(colorModeAgent).Bold(true)
	styleModePlan = lipgloss.NewStyle().Foreground(colorModePlan).Bold(true)
	styleModeAuto = lipgloss.NewStyle().Foreground(colorModeAuto).Bold(true)
	stylePickerBox = inkPanel(colorWash, colorHair, 0).Padding(0, 1)
	stylePaper = lipgloss.NewStyle().Foreground(colorBone).Background(colorPaper)
}

func tuiPrefsPath(mode paths.Mode) (string, error) {
	layout, err := paths.Resolve(mode)
	if err != nil {
		return "", err
	}
	return filepath.Join(layout.ConfigDir, tuiPrefsFilename), nil
}

func loadTheme(mode paths.Mode) ThemeName {
	path, err := tuiPrefsPath(mode)
	if err != nil {
		return defaultTheme
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return defaultTheme
	}
	var prefs tuiPrefs
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return defaultTheme
	}
	switch prefs.Theme {
	case ThemeDay, ThemeNight:
		return prefs.Theme
	default:
		return defaultTheme
	}
}

func saveTheme(mode paths.Mode, name ThemeName) error {
	if name != ThemeDay && name != ThemeNight {
		return fmt.Errorf("unknown theme %q", name)
	}
	path, err := tuiPrefsPath(mode)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(tuiPrefs{Theme: name}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}
