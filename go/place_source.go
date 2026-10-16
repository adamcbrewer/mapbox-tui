package main

// These values are persisted in favourites.json. Renaming a wire value requires
// a file-format migration; producers should use the constants rather than strings.
type placeSource string

const (
	placeSourceCoordinates     placeSource = "coordinates"
	placeSourceMapCentre       placeSource = "map centre"
	placeSourceMapPoint        placeSource = "map point"
	placeSourceMapboxSearch    placeSource = "Mapbox Search Box"
	placeSourceMapboxPermanent placeSource = "Mapbox Geocoding (permanent)"
)

func (source placeSource) valid() bool {
	switch source {
	case placeSourceCoordinates, placeSourceMapCentre, placeSourceMapPoint,
		placeSourceMapboxSearch, placeSourceMapboxPermanent:
		return true
	}
	return false
}

func (source placeSource) userEntered() bool {
	switch source {
	case placeSourceCoordinates, placeSourceMapCentre, placeSourceMapPoint:
		return true
	}
	return false
}
