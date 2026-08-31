package domain

type Player struct {
	Name            string         `json:"Name"`
	GuardianName    string         `json:"GuardianName"`
	PrimaryPhone    string         `json:"PrimaryPhone"`
	Scholarship     string         `json:"Scholarship"`
	BirthYear       int            `json:"BirthYear"`
	Status          string         `json:"Status"`
	Tournament      TournamentInfo `json:"Tournament"`
	VisoriaLocation string         `json:"VisoriaLocation"` // Este es el campo clave
}
