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

	hexNightPaper = "#10140F"
	hexNightInk   = "#181C17"
	hexNightWash  = "#242A23"
	hexNightHair  = "#4A5248"
	hexNightBone  = "#EDE8DC"
	hexNightFly   = "#8A9084"
	hexNightSeal  = "#C45C4A"
	hexNightMix   = "#F4F0E6"
	hexNightPine  = "#7A9E6E"
	hexNightWater = "#6A92B0"
	hexNightGold  = "#C9A45A"

	hexDayPaper = "#EDE6D6"
	hexDayInk   = "#F7F1E4"
	hexDayWash  = "#D9D0BE"
	hexDayHair  = "#8A8374"
	hexDayBone  = "#1A1916"
	hexDaySeal  = "#9B2D28"
	hexDayPine  = "#3D6B3A"
	hexDayWater = "#3A6A8A"
	hexDayGold  = "#A67C3A"
)

// Theme is the 青绿山水 palette (night ink-black / day xuan-paper).
type Theme struct {
	Name ThemeName

	Paper color.Color
	Hair  color.Color
	Bone  color.Color
	Fly   color.Color
	Seal  color.Color
	Mix   color.Color
	Pine  color.Color
	Water color.Color
	Gold  color.Color

	ModeAgent color.Color
	ModePlan  color.Color
	ModeAuto  color.Color
}

// Day: 宣纸昼 — warm paper, mineral accents.
var dayTheme = Theme{
	Name:      ThemeDay,
	Paper:     lipgloss.Color(hexDayPaper),
	Hair:      lipgloss.Color(hexDayHair),
	Bone:      lipgloss.Color(hexDayBone),
	Fly:       lipgloss.Color(hexDayHair),
	Seal:      lipgloss.Color(hexDaySeal),
	Mix:       lipgloss.Color(hexDayBone),
	Pine:      lipgloss.Color(hexDayPine),
	Water:     lipgloss.Color(hexDayWater),
	Gold:      lipgloss.Color(hexDayGold),
	ModeAgent: lipgloss.Color(hexDayWater),
	ModePlan:  lipgloss.Color(hexDayGold),
	ModeAuto:  lipgloss.Color(hexDaySeal),
}

// Night: 墨黑夜 — ink ground, 宣白 text, mineral accents.
var nightTheme = Theme{
	Name:      ThemeNight,
	Paper:     lipgloss.Color(hexNightPaper),
	Hair:      lipgloss.Color(hexNightHair),
	Bone:      lipgloss.Color(hexNightBone),
	Fly:       lipgloss.Color(hexNightFly),
	Seal:      lipgloss.Color(hexNightSeal),
	Mix:       lipgloss.Color(hexNightMix),
	Pine:      lipgloss.Color(hexNightPine),
	Water:     lipgloss.Color(hexNightWater),
	Gold:      lipgloss.Color(hexNightGold),
	ModeAgent: lipgloss.Color(hexNightWater),
	ModePlan:  lipgloss.Color(hexNightGold),
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
	colorHair = t.Hair
	colorBone = t.Bone
	colorFly = t.Fly
	colorSeal = t.Seal
	colorMix = t.Mix
	if t.Name == ThemeDay {
		colorBrush = t.Bone
	} else {
		colorBrush = t.Mix
	}

	colorDim = t.Fly
	colorOK = t.Pine
	colorWarn = t.Gold
	colorErr = t.Seal
	colorMuted = t.Fly
	colorTitle = t.Mix
	colorInput = t.Bone
	colorModeAgent = t.ModeAgent
	colorModePlan = t.ModePlan
	colorModeAuto = t.ModeAuto
	colorKeyword = t.Pine
	colorStampInk = t.Paper

	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(colorTitle)
	styleDim = lipgloss.NewStyle().Foreground(colorDim)
	styleMuted = lipgloss.NewStyle().Foreground(colorMuted)
	styleError = lipgloss.NewStyle().Foreground(colorErr)
	styleOK = lipgloss.NewStyle().Foreground(colorOK)
	styleWarn = lipgloss.NewStyle().Foreground(colorWarn)
	styleBadge = lipgloss.NewStyle().Foreground(colorSeal).Bold(true)
	styleInput = lipgloss.NewStyle().Foreground(colorInput).Background(colorPaper)
	styleKeyword = lipgloss.NewStyle().Foreground(colorKeyword).Bold(true)
	styleCompSel = lipgloss.NewStyle().Foreground(colorOK).Bold(true)
	styleComp = lipgloss.NewStyle().Foreground(colorDim)
	styleHelpBox = lipgloss.NewStyle().Foreground(colorBone).Background(colorPaper).Padding(0, 1)
	styleRiskHi = lipgloss.NewStyle().Foreground(colorErr).Bold(true)
	styleRiskMed = lipgloss.NewStyle().Foreground(colorWarn)
	styleRiskLo = lipgloss.NewStyle().Foreground(colorOK)
	styleTLUser = lipgloss.NewStyle().Foreground(colorSeal).Bold(true)
	styleTLSys = lipgloss.NewStyle().Foreground(colorDim)
	styleTLPlan = lipgloss.NewStyle().Foreground(colorModePlan)
	styleTLRun = lipgloss.NewStyle().Foreground(colorBone)
	styleTLErr = lipgloss.NewStyle().Foreground(colorErr)
	styleTLTool = lipgloss.NewStyle().Foreground(colorBone)
	styleTLReply = lipgloss.NewStyle().Foreground(colorBone)
	styleTLBody = lipgloss.NewStyle().Foreground(colorDim)
	styleTLJourney = lipgloss.NewStyle().Foreground(colorFly)
	styleMetricsTitle = lipgloss.NewStyle().Bold(true).Foreground(colorMix)
	stylePanelLabel = lipgloss.NewStyle().Foreground(colorFly)
	styleModeAgent = lipgloss.NewStyle().Foreground(colorModeAgent).Bold(true)
	styleModePlan = lipgloss.NewStyle().Foreground(colorModePlan).Bold(true)
	styleModeAuto = lipgloss.NewStyle().Foreground(colorModeAuto).Bold(true)
	stylePickerBox = lipgloss.NewStyle().Foreground(colorBone).Background(colorPaper).Padding(0, 1)
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
