// Copyright (c) the go-widgets authors. All rights reserved.
//
// SPDX-License-Identifier: BSD-3-Clause

//go:build !darwin

package tray

func setMainMenu(string, *Menu) error { return unsupportedMainMenu() }
