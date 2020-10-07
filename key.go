package termemu

import ()

type KeyMods int

const (
	KControl KeyMods = 1 << iota
	KAlt

	KMeta = KAlt
)

type EventKey struct {
	Rune rune
	Mods KeyMods
}
