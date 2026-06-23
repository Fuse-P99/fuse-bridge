package main

import (
	"fmt"
	"os"
	"path/filepath"
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
		tabWidget    *walk.TabWidget
		infoLb       *walk.Label
		logTE        *walk.TextEdit
		zoneTE       *walk.TextEdit
		snoopLB      *walk.ListBox
		snoopTE      *walk.TextEdit
		guildChatCB  *walk.CheckBox
		guildMotdCB  *walk.CheckBox
		broadcastsCB *walk.CheckBox
		serverMsgCB  *walk.CheckBox
		quakeMsgCB   *walk.CheckBox
		engageMsgCB  *walk.CheckBox
		whoOutputCB  *walk.CheckBox
		charLocCB    *walk.CheckBox
		autoStartCB  *walk.CheckBox
		eqDirLE      *walk.LineEdit
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

	buildZoneList := func() string {
		zones := GetAllZones()
		if len(zones) == 0 {
			return "No zone data yet."
		}
		toons := make([]string, 0, len(zones))
		for toon := range zones {
			toons = append(toons, toon)
		}
		slices.Sort(toons)
		var sb strings.Builder
		for _, toon := range toons {
			sb.WriteString(fmt.Sprintf("%-20s  %s\r\n", toon, zones[toon]))
		}
		return strings.TrimRight(sb.String(), "\r\n")
	}

	if err := (Dialog{
		AssignTo: &dlg,
		Title:    "Fuse Bridge — Settings",
		MinSize:  Size{Width: 560, Height: 440},
		Layout:   VBox{},
		Children: []Widget{
			TabWidget{
				AssignTo: &tabWidget,
				Pages: []TabPage{
					{
						Title:  "General",
						Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true},
						Children: []Widget{
							CheckBox{
								AssignTo: &autoStartCB,
								Text:     "Start automatically with Windows",
								Checked:  isAutoStartEnabled(),
							},
							VSeparator{},
							Label{Text: "EQ Install Directory:"},
							Composite{
								Layout: HBox{MarginsZero: true},
								Children: []Widget{
									LineEdit{
										AssignTo: &eqDirLE,
										Text:     s.EQDirectory,
										ReadOnly: true,
									},
									PushButton{
										Text: "Browse...",
										OnClicked: func() {
											cmd := noWindowCmd("powershell", "-NoProfile", "-NonInteractive", "-Command",
												`[void][System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');`+
													`$d=New-Object System.Windows.Forms.FolderBrowserDialog;`+
													`$d.Description='Select your EverQuest installation folder';`+
													`$d.RootFolder=[System.Environment+SpecialFolder]::MyComputer;`+
													`if($d.ShowDialog() -eq 'OK'){$d.SelectedPath}`)
											out, err := cmd.Output()
											if err != nil {
												return
											}
											path := strings.TrimSpace(string(out))
											if path == "" {
												return
											}
											if _, err := os.Stat(filepath.Join(path, "Logs")); err != nil {
												walk.MsgBox(settingsDlg, "Invalid folder",
													"The selected folder does not contain a Logs subfolder.\nPlease select your EverQuest installation folder.",
													walk.MsgBoxIconError|walk.MsgBoxOK)
												return
											}
											eqDirLE.SetText(path)
											cur := GetSettings()
											cur.EQDirectory = path
											UpdateSettings(cur)
										},
									},
								},
							},
							VSeparator{},
							Label{Text: "Fuse Bridge v" + clientVersion},
							VSpacer{},
						},
					},
					{
						Title:  "Status",
						Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true},
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
						Title:  "Character Locations",
						Layout: VBox{Alignment: AlignHNearVNear, MarginsZero: true},
						Children: []Widget{
							TextEdit{
								AssignTo: &zoneTE,
								Font:     Font{Family: "Courier New", PointSize: 9},
								Text:     buildZoneList(),
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
							CheckBox{
								AssignTo: &charLocCB,
								Text:     "Character locations",
								Checked:  s.CharacterLocations,
							},
							VSpacer{},
						},
					},
					{
						Title:  "Zone Snoop",
						Layout: HBox{MarginsZero: true},
						Children: []Widget{
							ListBox{
								AssignTo: &snoopLB,
								MinSize:  Size{Width: 200},
								MaxSize:  Size{Width: 200},
							},
							TextEdit{
								AssignTo: &snoopTE,
								ReadOnly: true,
								VScroll:  true,
								Font:     Font{Family: "Courier New", PointSize: 9},
							},
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

	// Clear selection whenever the user switches tabs so read-only TextEdits
	// don't appear with all text highlighted on first focus.
	tabWidget.CurrentIndexChanged().Attach(func() {
		logTE.SetTextSelection(0, 0)
		zoneTE.SetTextSelection(0, 0)
	})

	var snoopZones []zoneData
	snoopLB.CurrentIndexChanged().Attach(func() {
		idx := snoopLB.CurrentIndex()
		if idx < 0 || idx >= len(snoopZones) {
			snoopTE.SetText("")
			return
		}
		snoopTE.SetText(buildZoneContent(snoopZones[idx]))
		snoopTE.SetTextSelection(0, 0)
	})

	save := func() {
		current := GetSettings()
		UpdateSettings(Settings{
			GuildChat:          guildChatCB.Checked(),
			GuildMotd:          guildMotdCB.Checked(),
			Broadcasts:         broadcastsCB.Checked(),
			ServerMessages:     serverMsgCB.Checked(),
			QuakeMessages:      quakeMsgCB.Checked(),
			EngageMessages:     engageMsgCB.Checked(),
			WhoOutput:          whoOutputCB.Checked(),
			CharacterLocations: charLocCB.Checked(),
			StartupConfigured:  current.StartupConfigured,
			EQDirectory:        current.EQDirectory,
		})
	}
	guildChatCB.CheckedChanged().Attach(save)
	guildMotdCB.CheckedChanged().Attach(save)
	broadcastsCB.CheckedChanged().Attach(save)
	serverMsgCB.CheckedChanged().Attach(save)
	quakeMsgCB.CheckedChanged().Attach(save)
	engageMsgCB.CheckedChanged().Attach(save)
	whoOutputCB.CheckedChanged().Attach(save)
	charLocCB.CheckedChanged().Attach(save)
	autoStartCB.CheckedChanged().Attach(func() {
		if err := setAutoStart(autoStartCB.Checked()); err != nil {
			addStatus("Auto-start: %v", err)
		}
	})

	dlg.Closing().Attach(func(_ *bool, _ walk.CloseReason) {
		settingsDlg = nil
	})

	// Auto-refresh the Status and Character Locations tabs every 2 seconds.
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
				logTE.SetTextSelection(0, 0)
				zoneTE.SetText(buildZoneList())
				zoneTE.SetTextSelection(0, 0)
			})
		}
	}()

	go func() {
		doFetch := func() {
			zones, err := fetchZoneSnoop()
			if err != nil {
				return
			}
			trayOwner.Synchronize(func() {
				if settingsDlg == nil {
					return
				}
				// Remember the currently selected zone so we can restore it.
				prevName := ""
				if idx := snoopLB.CurrentIndex(); idx >= 0 && idx < len(snoopZones) {
					prevName = snoopZones[idx].Name
				}
				snoopZones = zones
				items := make([]string, len(zones))
				for i, z := range zones {
					items[i] = fmt.Sprintf("%s (%d)", z.Name, len(z.Characters))
				}
				snoopLB.SetModel(items)
				// Restore previous selection, or default to first zone.
				newIdx := 0
				for i, z := range zones {
					if z.Name == prevName {
						newIdx = i
						break
					}
				}
				if len(zones) > 0 {
					snoopLB.SetCurrentIndex(newIdx)
				}
			})
		}
		doFetch()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if settingsDlg == nil {
				return
			}
			doFetch()
		}
	}()

	dlg.Show()
}
