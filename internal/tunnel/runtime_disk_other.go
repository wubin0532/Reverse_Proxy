//go:build !linux && !darwin

package tunnel

import "fmt"

func runtimeFreeSpace(string) (uint64, error) { return 0, fmt.Errorf("不支持自动安装") }
