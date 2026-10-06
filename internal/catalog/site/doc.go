// Package site writes the static catalog site: four plain files that a web
// server, GitHub Pages or an internal static host can serve as they are.
//
//	index.html   the page, with the catalog data embedded
//	app.js       the interface (search, facets, overlaps, profiles)
//	style.css    light and dark themes, phone layout, reduced motion
//	catalog.json the same data as a separate file
//
// Security (SR5). The page is generated from embedded templates and is fully
// self-contained: no CDN, no web font, no remote request. Its Content
// Security Policy is default-src 'none' with only same-origin script, style,
// image and connect sources, so there is no unsafe-inline: the page has no
// inline script or style and no event handler attributes. The catalog data
// sits in a <script type="application/json"> block, which browsers never
// execute, with every "<" written as < so no text can close the block
// early. The script creates elements and sets textContent; it never parses
// an HTML string, and it makes a link only for an absolute http or https URL
// (checked again in JavaScript), always with rel="noopener noreferrer".
// Embedding the data lets the page work from file:// where browsers refuse
// to fetch a neighbouring JSON file.
//
// Files are created with mode 0600 and directories with 0700, written to a
// temporary name and renamed, and an existing symbolic link at a target path
// is refused instead of followed.
package site
