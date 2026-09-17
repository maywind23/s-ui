package util

import "strings"

// SurgeSnellTagPrefix marks a Snell inbound as a dedicated, single-client
// listener whose PSK is consumed directly by Surge. Ordinary Snell inbounds
// keep their existing sing-box multi-user behaviour.
const SurgeSnellTagPrefix = "SurgeSnell-"

func IsSurgeSnellInbound(inboundType, tag string) bool {
	return inboundType == "snell" && strings.HasPrefix(tag, SurgeSnellTagPrefix)
}
