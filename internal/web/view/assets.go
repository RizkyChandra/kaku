// Package view holds the admin UI's templ templates.
package view

// AssetVersion busts the immutable cache on /static assets. main overwrites it
// with the build version.
var AssetVersion = "dev"

func asset(path string) string { return "/static/" + path + "?v=" + AssetVersion }
