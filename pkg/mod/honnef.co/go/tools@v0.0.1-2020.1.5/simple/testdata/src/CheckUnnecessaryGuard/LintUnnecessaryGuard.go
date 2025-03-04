package pkg

func fn() {
	var m = map[string][]string{}

	if _, ok := m["k1"]; ok { // want `unnecessary guard around map access`
		m["k1"] = append(m["k1"], "v1", "v2")
	} else {
		m["k1"] = []string{"v1", "v2"}
	}

	if _, ok := m["k1"]; ok {
		m["k1"] = append(m["k1"], "v1", "v2")
	} else {
		m["k1"] = []string{"v1"}
	}

	if _, ok := m["k1"]; ok {
		m["k2"] = append(m["k2"], "v1")
	} else {
		m["k1"] = []string{"v1"}
	}

	k1 := "key"
	if _, ok := m[k1]; ok { // want `unnecessary guard around map access`
		m[k1] = append(m[k1], "v1", "v2")
	} else {
		m[k1] = []string{"v1", "v2"}
	}

	// ellipsis is not currently supported
	v := []string{"v1", "v2"}
	if _, ok := m["k1"]; ok {
		m["k1"] = append(m["k1"], v...)
	} else {
		m["k1"] = v
	}

	var m2 map[string]int
	if _, ok := m2["k"]; ok { // want `unnecessary guard around map access`
		m2["k"] += 4
	} else {
		m2["k"] = 4
	}

	if _, ok := m2["k"]; ok {
		m2["k"] += 4
	} else {
		m2["k"] = 3
	}

	if _, ok := m2["k"]; ok { // want `unnecessary guard around map access`
		m2["k"]++
	} else {
		m2["k"] = 1
	}

	if _, ok := m2["k"]; ok {
		m2["k"] -= 1
	} else {
		m2["k"] = 1
	}
}

// this used to cause a panic in the pattern package
func fn2() {
	var obj interface{}

	if _, ok := obj.(map[string]interface{})["items"]; ok {
		obj.(map[string]interface{})["version"] = 1
	}
}
