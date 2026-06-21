package main

import (
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

var settingsDialog *walk.Dialog

// openSettingsWindow shows the settings dialog if it isn't already open.
// Subsequent calls bring the existing window to the foreground.
func openSettingsWindow() {
	if settingsDialog != nil {
		settingsDialog.BringToTop()
		return
	}

	s := GetSettings()

	var (
		dlg          *walk.Dialog
		guildChatCB  *walk.CheckBox
		guildMotdCB  *walk.CheckBox
		broadcastsCB *walk.CheckBox
		serverMsgCB  *walk.CheckBox
		quakeMsgCB   *walk.CheckBox
		engageMsgCB  *walk.CheckBox
		whoOutputCB  *walk.CheckBox
	)

	if err := (Dialog{
		AssignTo: &dlg,
		Title:    "Fuse Bridge — Settings",
		MinSize:  Size{Width: 380, Height: 260},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				Pages: []TabPage{
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
						Text: "Close",
						OnClicked: func() {
							dlg.Close(0)
						},
					},
				},
			},
		},
	}.Create(nil)); err != nil {
		return
	}

	settingsDialog = dlg
	applyDialogIcon(dlg)

	// Save on any checkbox change
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

	dlg.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		settingsDialog = nil
	})

	dlg.Show()
}
