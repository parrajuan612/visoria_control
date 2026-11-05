package domain

type Pricing struct {
	Total string
	Pago1 string
	Pago2 string
	Pago3 string
}

type TournamentInfo struct {
	Name     string
	Category string
	Pricing  Pricing
}
