package domain

type Player struct {
	Name            string         `json:"Name"`
	Club            string         `json:"Club"`
	GuardianName    string         `json:"GuardianName"`
	PrimaryPhone    string         `json:"PrimaryPhone"`
	Scholarship     string         `json:"Scholarship"`
	BirthYear       int            `json:"BirthYear"`
	BirthDate       string         `json:"BirthDate"` // 👉 NUEVO CAMPO
	Status          string         `json:"Status"`
	Tournament      TournamentInfo `json:"Tournament"`
	VisoriaLocation string         `json:"VisoriaLocation"`
	PaymentDate1    string         `json:"PaymentDate1"`
	PaymentDate2    string         `json:"PaymentDate2"`
	PaymentDate3    string         `json:"PaymentDate3"`
	FileID          string         `json:"FileID"`
}
