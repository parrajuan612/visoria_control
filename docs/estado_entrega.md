# Estado de la entrega — Visoria Control v2

## 1. Dónde estamos
| Parte | Estado |
|---|---|
| Sincronizar la hoja de Google (becas, años, torneos) | Hecho y probado |
| Subir Excel, validar y editar celdas en la tabla | Hecho y probado |
| Generar PDF desde las 3 plantillas Word (LibreOffice) | Hecho y probado |
| Envío por **WhatsApp** (`purchase_receipt_3`) | Hecho. Probado con un envío real: Meta lo aceptó |
| Envío por **correo** (Gmail SMTP) | Hecho y probado con servidor simulado. **Falta la prueba real** (necesita la contraseña de aplicación) |
| Pantalla del paso 3 (progreso, reenviar, reanudar) | Hecho |
| Entregado/Leído de WhatsApp, rebotes de correo, recordatorios, login | **No incluido** (ver `docs/operacion.md`) |

«Aceptado» = el proveedor recibió el mensaje, no que el acudiente lo recibió.

## 2. Pasos al llegar a casa
### 2.1 Requisitos
- Git, **Go 1.26**, **Docker**, **LibreOffice** (`sudo apt install libreoffice-writer`).
- Si LibreOffice es de *snap*, define `DOCS_WORKDIR` (paso 2.3) con una carpeta de tu home que no esté oculta.

### 2.2 Clonar
```
git clone https://github.com/parrajuan612/visoria_control.git
cd visoria_control
cp .env.example .env
```

### 2.3 Completar `.env` (nunca se sube a git)
- `WHATSAPP_TOKEN`, `WHATSAPP_PHONE_ID`, `CHATWOOT_*`: cópialos de tu otro equipo o de Meta/Chatwoot.
- `SMTP_USER` = tu Gmail; `SMTP_APP_PASSWORD` = contraseña de aplicación de 16 letras (Cuenta de Google → Seguridad → Verificación en 2 pasos → Contraseñas de aplicaciones).
- `REDIS_ADDR=127.0.0.1:6380` y `REDIS_PASSWORD` igual a la de `docker-compose.yml`.
- `DATA_DIR=./data` y, si usas LibreOffice por snap, `DOCS_WORKDIR=/home/<tu_usuario>/visoria_work`.
- **Para la prueba:** `DISPATCH_ALLOWLIST=573209323449,<tu_correo>` (solo se envía a esos).

### 2.4 Levantar
```
docker compose up -d
go build -o visoria ./cmd/visoria && ./visoria     # http://localhost:8880
```
Debe avisar «DISPATCH_ALLOWLIST activa». Si avisa «correo deshabilitado», faltan las credenciales de Gmail.

### 2.5 Archivos que NO están en el repo (datos del cliente)
Copia desde el otro equipo: las 3 plantillas `.docx` y un Excel de prueba.

### 2.6 Prueba de punta a punta (solo tu móvil y tu correo)
1. ⚙ **Configuración** → *Sincronizar ahora*.
2. En la misma pantalla, **sube las 3 plantillas** (Cadete/Juvenil, Benjamín/Alevín, Infantil).
3. Excel de 1 fila con **tu** móvil y **tu** correo, y año de nacimiento de 4 dígitos.
4. Paso 1: torneo y fechas de pago → Paso 2: sube el Excel y corrige lo marcado en rojo.
5. Paso 3: *Generar propuestas* → *Enviar propuestas* (WhatsApp y Correo).
6. Comprueba que llegó el WhatsApp con el PDF y el correo con el PDF adjunto y el botón de WhatsApp.
7. Estado esperado: «Aceptado por WhatsApp» y «Aceptado por Gmail».

### 2.7 Pasar a producción (mañana)
1. Quita `DISPATCH_ALLOWLIST` del `.env` y reinicia.
2. Sube el Excel real, revisa la tabla y confirma el aviso de envío (es real).
3. Campaña «en pausa»: token de WhatsApp vencido o contraseña de Gmail incorrecta → corrige `.env`, reinicia, *Reanudar*.
4. Fallos permanentes (número sin WhatsApp, correo inexistente): crea una campaña nueva con esas filas.

## 3. Pendientes de seguridad
- **Rotar** los tokens de WhatsApp y Chatwoot: estuvieron en el historial de git.
- El historial contiene PDFs de menores (`uploads/pdfs`): reescribir el historial o hacer el repo privado.
- La API no tiene login: úsala solo en red de confianza o tras nginx con contraseña.
