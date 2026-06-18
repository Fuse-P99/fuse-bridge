package main

import (
	"fmt"
	"strings"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

var statusDlg *walk.Dialog

func openStatusWindow() {
	if statusDlg != nil {
		statusDlg.BringToTop()
		return
	}

	var (
		dlg    *walk.Dialog
		infoLb *walk.Label
		logTE  *walk.TextEdit
	)

	buildInfo := func() string {
		eq, lf, conn, _ := getStatusSnapshot()
		eqStr := "Not detected"
		if eq {
			eqStr = "Running"
		}
		connStr := "Not connected"
		if conn {
			connStr = "Connected"
		}
		lfStr := "None"
		if lf != "" {
			lfStr = lf
		}
		return fmt.Sprintf("EverQuest: %s\r\nLog File:  %s\r\nServer:    %s  (%s)", eqStr, lfStr, connStr, serverURL)
	}

	buildActivity := func() string {
		_, _, _, lines := getStatusSnapshot()
		return strings.Join(lines, "\r\n")
	}

	if err := (Dialog{
		AssignTo: &dlg,
		Title:    "Fuse Bridgekeeper Relay — Status",
		MinSize:  Size{Width: 560, Height: 440},
		Layout:   VBox{},
		Children: []Widget{
			Label{
				AssignTo: &infoLb,
				Text:     buildInfo(),
			},
			VSeparator{},
			TextEdit{
				AssignTo: &logTE,
				Text:     buildActivity(),
				ReadOnly: true,
				VScroll:  true,
				MinSize:  Size{Height: 300},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					PushButton{
						Text: "Refresh",
						OnClicked: func() {
							infoLb.SetText(buildInfo())
							logTE.SetText(buildActivity())
						},
					},
					PushButton{
						Text: "Close",
						OnClicked: func() { dlg.Close(0) },
					},
				},
			},
		},
	}.Create(nil)); err != nil {
		return
	}

	statusDlg = dlg
	dlg.Closing().Attach(func(_ *bool, _ walk.CloseReason) {
		statusDlg = nil
	})
	dlg.Show()
}
