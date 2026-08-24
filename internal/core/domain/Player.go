package domain

type Player struct {
	Name         string
	GuardianName string
	PrimaryPhone string
	Scholarship  string
	BirthYear    int
	Status       string
	Tournament   TournamentInfo // <- Agregamos esta línea
}
