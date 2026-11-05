package domain

type Player struct {
	Name            string         `json:"Name"`
	Club            string         `json:"Club"`
	GuardianName    string         `json:"GuardianName"`
	PrimaryPhone    string         `json:"PrimaryPhone"` // móvil: 10 dígitos si es colombiano; si no, internacional
	Email           string         `json:"Email"`
	Scholarship     string         `json:"Scholarship"`
	BirthYear       int            `json:"BirthYear"`
	BirthDate       string         `json:"BirthDate"` // año de nacimiento (el Excel ya solo trae el año)
	Status          string         `json:"Status"`
	Tournament      TournamentInfo `json:"Tournament"`
	VisoriaLocation string         `json:"VisoriaLocation"`
	PaymentDate1    string         `json:"PaymentDate1"`
	PaymentDate2    string         `json:"PaymentDate2"`
	PaymentDate3    string         `json:"PaymentDate3"`
	FileID          string         `json:"FileID"`
	Issues          []Issue        `json:"Issues"` // hallazgos de la validación (errores y advertencias)
}
