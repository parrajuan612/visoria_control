// Simulador de carga inicial (Splash Screen)
window.addEventListener('load', () => {
    setTimeout(() => {
        document.getElementById('splash').classList.remove('step-flex'); // AQUÍ ESTÁ EL FIX
        document.getElementById('splash').classList.add('step-hidden');
        
        document.getElementById('step1').classList.remove('step-hidden');
        document.getElementById('step1').classList.add('step-active');
    }, 1500);
});

// Navegación entre pasos
async function goToStep(step) {
    if (step === 2) {
        // El torneo de origen es obligatorio (va en el encabezado de cada propuesta).
        if (!document.getElementById('torneo').value.trim()) {
            alert("Escribe el nombre del torneo donde se becó a los jugadores.");
            document.getElementById('torneo').focus();
            return;
        }
        // Antes de pasar al paso 2, cargamos la configuración maestra
        const btn = document.querySelector('#step1 button');
        const originalText = btn.innerHTML;
        btn.innerHTML = "Cargando configuración...";
        btn.disabled = true;

        const restoreButton = () => { btn.innerHTML = originalText; btn.disabled = false; };
        try {
            // La URL de la hoja la gestiona el servidor (entorno o la última guardada).
            const result = await syncConfig();
            if (!result.ok) {
                // Si ya hay una configuración válida vigente se puede continuar con ella;
                // si no, se abre la pantalla de configuración con el motivo.
                const current = await getConfig().catch(() => null);
                const puedeContinuar = result.status !== 400 && current && current.sincronizada;
                if (!puedeContinuar) {
                    restoreButton();
                    openConfigPanel(result);
                    return;
                }
                const sigue = confirm(
                    (result.data.error || "No se pudo actualizar la configuración.") +
                    `\n\nSe usará la última configuración válida (versión ${current.version}, ` +
                    `${new Date(current.sincronizada_en).toLocaleString('es-CO')}).\n¿Continuar?`);
                if (!sigue) {
                    restoreButton();
                    openConfigPanel(result);
                    return;
                }
            }
            console.log("Configuración maestra lista.");
        } catch (error) {
            alert("Error al cargar configuración: " + error.message);
            restoreButton();
            return; // No lo dejamos avanzar si falla
        }

        btn.innerHTML = originalText;
        btn.disabled = false;
    }

    if (step === 3 && !(await prepareStep3())) return;

    document.getElementById('step1').classList.replace('step-active', 'step-hidden');
    document.getElementById('step2').classList.replace('step-active', 'step-hidden');
    document.getElementById('step3').classList.replace('step-active', 'step-hidden');
    
    document.getElementById('step' + step).classList.replace('step-hidden', 'step-active');
}

// Variable global para guardar los niños mientras avanzamos por los pasos
window.currentPlayers = [];

// Fechas de pago de las casillas del paso 1 (el servidor valida orden y huecos).
function paymentDateFields() {
    return {
        fecha_pago_1: document.getElementById('date1').value,
        fecha_pago_2: document.getElementById('date2').value,
        fecha_pago_3: document.getElementById('date3').value,
    };
}

// Escuchar cuando el usuario arrastra/selecciona el Excel: se crea una campaña (borrador validado).
document.getElementById('excelUpload').addEventListener('change', async (e) => {
    const file = e.target.files[0];
    if (!file) return;

    try {
        const result = await createCampaign(file, {
            torneo: document.getElementById('torneo').value,
            requerir_ambos: document.getElementById('requireBoth').checked ? 'true' : 'false',
            ...paymentDateFields(),
        });
        if (!result.ok) throw new Error(result.data.error || 'No se pudo procesar el archivo.');

        window.currentCampaign = result.data.campaign;
        window.currentPlayers = [];
        campaignView.show(result.data);
    } catch (error) {
        alert("Error: " + error.message);
    } finally {
        e.target.value = ''; // permite volver a subir el mismo archivo ya corregido
    }
});

// Antes del paso 3: avisa de lo que no se enviará y abre el panel de generación y envío.
async function prepareStep3() {
    const campaign = window.currentCampaign;
    if (!campaign) {
        alert("Primero sube el Excel de jugadores.");
        return false;
    }
    const r = campaignView.current().resumen;
    if (r.validas === 0) {
        alert("No hay filas válidas para enviar. Corrige las filas con errores en la tabla.");
        return false;
    }
    if (r.con_errores > 0) {
        const sigue = confirm(`${r.con_errores} fila(s) tienen errores y NO se enviarán hasta corregirlas.\n` +
            `Se procesarán ${r.validas} fila(s) válidas.\n\n¿Continuar de todas formas?`);
        if (!sigue) return false;
    }
    sendPanel.open(campaign.id);
    return true;
}
