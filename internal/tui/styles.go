package tui

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

var (
	colorPaper    color.Color
	colorInk      color.Color
	colorHair     color.Color
	colorBone     color.Color
	colorFly      color.Color
	colorSeal     color.Color
	colorMix      color.Color
	colorBrush    color.Color
	colorStampInk color.Color

	colorDim       color.Color
	colorOK        color.Color
	colorWarn      color.Color
	colorErr       color.Color
	colorMuted     color.Color
	colorTitle     color.Color
	colorInput     color.Color
	colorModeAgent color.Color
	colorModePlan  color.Color
	colorModeAuto  color.Color
	colorKeyword   color.Color

	styleTitle        lipgloss.Style
	styleDim          lipgloss.Style
	styleMuted        lipgloss.Style
	styleError        lipgloss.Style
	styleOK           lipgloss.Style
	styleWarn         lipgloss.Style
	styleBadge        lipgloss.Style
	styleInput        lipgloss.Style
	styleKeyword      lipgloss.Style
	styleCompSel      lipgloss.Style
	styleComp         lipgloss.Style
	styleHelpBox      lipgloss.Style
	styleRiskHi       lipgloss.Style
	styleRiskMed      lipgloss.Style
	styleRiskLo       lipgloss.Style
	styleTLUser       lipgloss.Style
	styleTLSys        lipgloss.Style
	styleTLPlan       lipgloss.Style
	styleTLRun        lipgloss.Style
	styleTLErr        lipgloss.Style
	styleTLTool       lipgloss.Style
	styleTLReply      lipgloss.Style
	styleTLBody       lipgloss.Style
	styleTLJourney    lipgloss.Style
	styleMetricsTitle lipgloss.Style
	stylePanelLabel   lipgloss.Style
	styleModeAgent    lipgloss.Style
	styleModePlan     lipgloss.Style
	styleModeAuto     lipgloss.Style
	stylePickerBox    lipgloss.Style
	stylePaper        lipgloss.Style
)

func init() {
	applyTheme(nightTheme)
}

func sseDot(state string) string {
	switch state {
	case "ok":
		return styleOK.Render("●")
	case "reconnecting", "connecting":
		return styleWarn.Render("○")
	default:
		return styleError.Render("●")
	}
}

func stateBadge(state string) string {
	switch state {
	case "completed", "approved":
		return styleOK.Render(state)
	case "failed", "cancelled":
		return styleError.Render(state)
	case "paused":
		return styleWarn.Render(state)
	case "running":
		return styleBadge.Render(state)
	default:
		return styleDim.Render(state)
	}
}

func riskStyle(risk string) lipgloss.Style {
	switch risk {
	case "high", "critical":
		return styleRiskHi
	case "medium", "moderate":
		return styleRiskMed
	default:
		return styleRiskLo
	}
}
