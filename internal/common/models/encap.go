package models

// EffectiveEncapType returns the data-plane encap type after applying the
// legacy default used by REST and protobuf config paths.
func EffectiveEncapType(encapType EncapType) EncapType {
	if encapType == "" {
		return EncapTypeL3DSR
	}
	return encapType
}

// IsValidEncapType reports whether encapType is one of the supported non-empty
// encapsulation modes.
func IsValidEncapType(encapType EncapType) bool {
	switch encapType {
	case EncapTypeGRE4, EncapTypeGRE6, EncapTypeL3DSR, EncapTypeNAT4, EncapTypeNAT6:
		return true
	default:
		return false
	}
}

// EncapRequiresIPv4VIP reports whether the VIP address must be IPv4.
func EncapRequiresIPv4VIP(encapType EncapType) bool {
	switch encapType {
	case EncapTypeL3DSR, EncapTypeNAT4:
		return true
	default:
		return false
	}
}

// EncapRequiresIPv6VIP reports whether the VIP address must be IPv6.
func EncapRequiresIPv6VIP(encapType EncapType) bool {
	return encapType == EncapTypeNAT6
}

// EncapRequiresIPv4Backend reports whether backend addresses must be IPv4.
func EncapRequiresIPv4Backend(encapType EncapType) bool {
	switch encapType {
	case EncapTypeGRE4, EncapTypeL3DSR, EncapTypeNAT4:
		return true
	default:
		return false
	}
}

// EncapRequiresIPv6Backend reports whether backend addresses must be IPv6.
func EncapRequiresIPv6Backend(encapType EncapType) bool {
	switch encapType {
	case EncapTypeGRE6, EncapTypeNAT6:
		return true
	default:
		return false
	}
}
