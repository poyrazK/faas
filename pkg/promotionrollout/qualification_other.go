//go:build !linux

package promotionrollout

func nativeQualificationHost(string) bool { return false }
