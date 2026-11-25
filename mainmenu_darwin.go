// Copyright (c) the go-widgets authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build darwin

package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	objc "github.com/go-macos/objc"
)

// The application's main menu: -[NSApplication setMainMenu:].
//
// It reuses everything the status item's menu already is: the same buildMenu
// (key equivalents, icons, checkmarks, submenus), the same click table indexed
// by tag, the same signature for change-only re-application. What it adds is
// the application menu AppKit requires first in a main menu, and a hop to the
// main thread so any goroutine may call it.
//
// References: Apple, "NSMenu" and "NSApplication.mainMenu"
// (developer.apple.com/documentation/appkit/nsapplication/1428634-mainmenu);
// Apple, "Human Interface Guidelines: The menu bar", for the app/Edit layout.

type mainMenuState struct {
	mu      sync.Mutex
	once    sync.Once
	prepErr error
	b       *darwinBackend

	name  string
	menus *Menu
	// applied is the signature of what is installed; "" before the first apply.
	applied string
	err     error
}

var mainMenu mainMenuState

// The seams a test replaces to reach the refusals a working Mac never takes.
var (
	mainMenuLoadAppKit = ensureAppKit
	mainMenuApp        = objc.App
	mainMenuTarget     = registerMainMenuTarget
)

// registerMainMenuTarget makes the Objective-C class that carries a row's click
// and the apply hop, and one instance of it. objc_allocateClassPair refuses a
// name it has already seen, so a second call is an error, not a second class.
func registerMainMenuTarget(s *mainMenuState, b *darwinBackend) (objc.ID, error) {
	cls, err := objc.RegisterClass("GoWidgetsMainMenuTarget", objc.GetClass("NSObject"), []objc.MethodDef{
		{Cmd: objc.Sel("handle:"), Fn: func(self objc.ID, _ objc.SEL, sender objc.ID) {
			s.mu.Lock()
			items := b.items
			s.mu.Unlock()
			dispatchLeaf(items, int(sender.Send(objc.Sel("tag"))))
		}},
		{Cmd: objc.Sel("goMainMenuApply:"), Fn: func(self objc.ID, _ objc.SEL, _ objc.ID) {
			objc.AutoreleasePool(s.applyOnMain)
		}},
	})
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrNoTargetClass, err)
	}
	return objc.ID(cls).Send(objc.Sel("alloc")).Send(objc.Sel("init")), nil
}

// prepare loads AppKit and makes the target, once; its outcome is remembered.
func (s *mainMenuState) prepare() error {
	s.once.Do(func() {
		if s.prepErr = mainMenuLoadAppKit(); s.prepErr != nil {
			return
		}
		b := &darwinBackend{}
		target, err := mainMenuTarget(s, b)
		s.prepErr = mainMenuTargetError(uintptr(target), err)
		b.target = target
		s.b = b
	})
	return s.prepErr
}

func setMainMenu(appName string, menus *Menu) error {
	s := &mainMenu
	if err := s.prepare(); err != nil {
		return err
	}
	if appName == "" {
		appName = filepath.Base(os.Args[0])
	}
	s.mu.Lock()
	s.name, s.menus, s.err = appName, menus, nil
	s.mu.Unlock()
	// Waits: on the main thread Cocoa runs it inline, from any other it is queued
	// on the running main loop, and the answer is the one this call asked for.
	s.b.runOnMain(objc.Sel("goMainMenuApply:"))
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *mainMenuState) applyOnMain() {
	s.mu.Lock()
	name, menus := s.name, s.menus
	s.mu.Unlock()

	app := mainMenuApp()
	if err := mainMenuAppError(uintptr(app)); err != nil {
		s.fail(err)
		return
	}
	full := &Menu{Items: []*MenuItem{SubMenu(name, appMenu(name))}}
	if menus != nil {
		full.Items = append(full.Items, menus.Items...)
	}
	// Always refreshed: it carries the CURRENT handlers, which the signature
	// deliberately ignores.
	items := leafItems(full)
	s.mu.Lock()
	s.b.items = items
	s.mu.Unlock()

	sig := full.Signature()
	if sig == s.applied {
		return
	}
	s.b.tagSeq = 0
	menu := s.b.buildMenu(full)
	app.Send(objc.Sel("setMainMenu:"), menu)
	menu.Send(objc.Sel("release")) // setMainMenu: retains
	s.applied = sig
}

func (s *mainMenuState) fail(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// appMenu is the menu macOS puts first: About, Hide, Hide Others, Show All, Quit.
func appMenu(name string) *Menu {
	about := RoleItem(RoleAbout)
	about.Label = "About " + name
	hide := RoleItem(RoleHide)
	hide.Label = "Hide " + name
	quit := RoleItem(RoleQuit)
	quit.Label = "Quit " + name
	return NewMenu().Add(about, Separator(), hide, RoleItem(RoleHideOthers), RoleItem(RoleShowAll), Separator(), quit)
}
