package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

var settingsDlg *walk.Dialog

func openSettingsWindow() {
	if settingsDlg != nil {
		settingsDlg.BringToTop()
		return
	}

	s := GetSettings()

	var (
		dlg          *walk.Dialog
		infoLb       *walk.Label
		logTE        *walk.TextEdit
		guildChatCB  *walk.CheckBox
		guildMotdCB  *walk.CheckBox
		broadcastsCB *walk.CheckBox
		serverMsgCB  *walk.CheckBox
		quakeMsgCB   *walk.CheckBox
		engageMsgCB  *walk.CheckBox
		whoOutputCB  *walk.CheckBox
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
		Title:    "Fuse Bridge — Settings",
		MinSize:  Size{Width: 560, Height: 440},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				Pages: []TabPage{
					{
						Title:  "Status",
						Layout: VBox{Alignment: AlignHNearVNear},
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
							},
						},
					},
					{
						Title:  "Filters",
						Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true},
						Children: []Widget{
							CheckBox{
								AssignTo: &guildChatCB,
								Text:     "Guild chat",
								Checked:  s.GuildChat,
							},
							CheckBox{
								AssignTo: &guildMotdCB,
								Text:     "Guild MOTD",
								Checked:  s.GuildMotd,
							},
							CheckBox{
								AssignTo: &broadcastsCB,
								Text:     "GM Broadcasts",
								Checked:  s.Broadcasts,
							},
							CheckBox{
								AssignTo: &serverMsgCB,
								Text:     "Server Messages",
								Checked:  s.ServerMessages,
							},
							CheckBox{
								AssignTo: &quakeMsgCB,
								Text:     "Quake messages",
								Checked:  s.QuakeMessages,
							},
							CheckBox{
								AssignTo: &engageMsgCB,
								Text:     "Engage messages",
								Checked:  s.EngageMessages,
							},
							CheckBox{
								AssignTo: &whoOutputCB,
								Text:     "/who output",
								Checked:  s.WhoOutput,
							},
						},
					},
					{
						Title:  "Info",
						Layout: VBox{Alignment: AlignHNearVNear},
						Children: []Widget{
							Label{Text: "Fuse Bridge"},
							Label{Text: "Version: " + clientVersion},
						},
					},
				},
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

	settingsDlg = dlg
	applyDialogIcon(dlg)

	save := func() {
		UpdateSettings(Settings{
			GuildChat:      guildChatCB.Checked(),
			GuildMotd:      guildMotdCB.Checked(),
			Broadcasts:     broadcastsCB.Checked(),
			ServerMessages: serverMsgCB.Checked(),
			QuakeMessages:  quakeMsgCB.Checked(),
			EngageMessages: engageMsgCB.Checked(),
			WhoOutput:      whoOutputCB.Checked(),
		})
	}
	guildChatCB.CheckedChanged().Attach(save)
	guildMotdCB.CheckedChanged().Attach(save)
	broadcastsCB.CheckedChanged().Attach(save)
	serverMsgCB.CheckedChanged().Attach(save)
	quakeMsgCB.CheckedChanged().Attach(save)
	engageMsgCB.CheckedChanged().Attach(save)
	whoOutputCB.CheckedChanged().Attach(save)

	dlg.Closing().Attach(func(_ *bool, _ walk.CloseReason) {
		settingsDlg = nil
	})

	// Auto-refresh the Status tab every 2 seconds.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if settingsDlg == nil {
				return
			}
			trayOwner.Synchronize(func() {
				if settingsDlg == nil {
					return
				}
				infoLb.SetText(buildInfo())
				logTE.SetText(buildActivity())
			})
		}
	}()

	dlg.Show()
}
