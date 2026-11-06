//go:build !darwin

package main

func setWindowCornerRadius(_ float64) {}

func (a *App) IsWindowActive() bool { return true }
