# Guía de operación — Visoria Control v2

## Requisitos
- **Go 1.26** (para compilar) y **Docker** (Redis propio: `docker compose up -d`).
- **LibreOffice** para convertir las propuestas a PDF (`apt install libreoffice-writer`).
  Si viene por *snap* no puede leer `/tmp`: defina `DOCS_WORKDIR` con una carpeta de su home (no oculta).
- `pdftotext` es opcional (solo para las pruebas).

## Configuración (`.env`, ver `.env.example`)
| Variable | Para qué |
|---|---|
| `WHATSAPP_TOKEN`, `WHATSAPP_PHONE_ID` | WhatsApp Cloud API (plantilla `purchase_receipt_3`, ya aprobada) |
| `SMTP_USER`, `SMTP_APP_PASSWORD` | Correo por Gmail. Es la **contraseña de aplicación** de 16 letras, no la normal |
| `CHATWOOT_*` | Notas privadas en Chatwoot (no bloquean el envío) |
| `REDIS_ADDR`, `REDIS_PASSWORD` | Redis propio (compose: `127.0.0.1:6380`) |
| `DATA_DIR` | Excel originales, PDF, plantillas y auditoría de cada campaña (contiene datos de menores) |
| `SHEETS_CSV_URL` | Hoja de Google publicada como CSV (también se cambia desde la pantalla) |
| `DISPATCH_ALLOWLIST` | **Para pruebas:** si se define, SOLO se envía a esos móviles/correos. Déjela vacía en producción |

## Arranque
```
docker compose up -d
go build -o visoria ./cmd/visoria && ./visoria      # abre http://localhost:8880
```
Un solo proceso hace de API, worker y programador (`--roles=api,worker,scheduler`).

## Primer uso
1. **⚙ Configuración** → «Sincronizar ahora» (la hoja de Google debe estar publicada como CSV).
2. En la misma pantalla, **suba las 3 plantillas Word** (Cadete/Juvenil, Benjamín/Alevín, Infantil). Se validan antes de reemplazar y la anterior queda guardada.

## Cada campaña
1. **Paso 1:** torneo donde se becó y fechas de pago (en orden; cada beca usa las primeras que necesite).
2. **Paso 2:** subir el Excel. Corrija las celdas en rojo directamente en la tabla (Enter guarda).
3. **Paso 3:** *Generar propuestas* → *Enviar propuestas* (WhatsApp y/o correo). Se ve el estado real de cada envío.
   *Reenviar* vuelve a intentar lo que falló por motivos no permanentes.

## Qué significa cada estado
- **Aceptado por WhatsApp / por Gmail:** el proveedor recibió el mensaje. **No** confirma que el acudiente lo recibió.
- **No entregado:** error definitivo con el motivo (número sin WhatsApp, dirección inexistente…). Hay que corregir el dato.
- **Revisar:** el proceso se interrumpió a mitad de un envío y no se sabe si salió. **Nunca se reenvía solo**: revise y use *Reenviar*.
- **Omitido:** la fila no tiene ese dato (sin móvil / sin correo) o quedó fuera de la lista de pruebas.
- **Campaña en pausa:** casi siempre el token de WhatsApp venció o la contraseña de Gmail es incorrecta. Corríjalo en `.env`, reinicie y pulse *Reanudar*.
- **Límite diario de Gmail (450):** los correos restantes salen solos al día siguiente a las 08:00 (Bogotá).

## Limitaciones actuales
- **Sin inicio de sesión:** use el sistema solo en una red de confianza o detrás de una autenticación de nginx.
- **Sin confirmación de entrega de WhatsApp** (Entregado/Leído): requiere cambiar el webhook de Meta, que hoy apunta a Chatwoot.
- **Rebotes de correo:** llegan a la bandeja de Gmail y se corrigen a mano (no se detectan automáticamente).
- **Sin recordatorios de pago automáticos** (siguiente fase).
- Después de despachar, la campaña ya no se edita; para corregir un dato permanente cree una campaña nueva con esas filas.
