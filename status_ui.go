package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

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
		return fmt.Sprintf("EverQuest: %s\r\nLog File:  %s\r\nServer:    %s", eqStr, lfStr, connStr)
	}

	buildActivity := func() string {
		_, _, _, lines := getStatusSnapshot()
		slices.Reverse(lines)
		return strings.Join(lines, "\r\n")
	}

	if err := (Dialog{
		AssignTo: &dlg,
		Title:    "Fuse Bridge — Status",
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
						Text:      "Close",
						OnClicked: func() { dlg.Close(0) },
					},
				},
			},
		},
	}.Create(nil)); err != nil {
		return
	}

	statusDlg = dlg
	applyDialogIcon(dlg)
	dlg.Closing().Attach(func(_ *bool, _ walk.CloseReason) {
		statusDlg = nil
	})

	// Auto-refresh every 2 seconds using a background goroutine that
	// marshals updates onto the walk message loop via Synchronize.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if statusDlg == nil {
				return
			}
			trayOwner.Synchronize(func() {
				if statusDlg == nil {
					return
				}
				infoLb.SetText(buildInfo())
				logTE.SetText(buildActivity())
			})
		}
	}()

	dlg.Show()
}
