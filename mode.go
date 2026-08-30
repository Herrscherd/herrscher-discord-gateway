package discord

const (
	normalMode  = "normal"
	supportMode = "support"
	ambientMode = "ambient"
	offMode     = "off"
)

func knownMode(v string) bool {
	switch v {
	case normalMode, supportMode, ambientMode, offMode:
		return true
	}
	return false
}
