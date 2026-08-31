package api

import (
	"strconv"
	"strings"
)

// normalizeVersion 去掉前导 v 与预发布/构建后缀（"v1.2.3-beta" → "1.2.3"）。
func normalizeVersion(version string) string {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if index := strings.IndexAny(version, "-+"); index >= 0 {
		version = version[:index]
	}
	return version
}

// compareVersions 比较点分数字版本，返回 -1/0/1。两段与三段混用时按缺省 0 补齐；
// 非数字段按 0 处理（客户端 Tauri updater 会再做一次严格 semver 校验，这里只需粗判）。
func compareVersions(a, b string) int {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		valueA, valueB := 0, 0
		if i < len(partsA) {
			valueA, _ = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			valueB, _ = strconv.Atoi(partsB[i])
		}
		if valueA != valueB {
			if valueA < valueB {
				return -1
			}
			return 1
		}
	}
	return 0
}
