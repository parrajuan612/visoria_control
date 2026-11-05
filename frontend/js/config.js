// Pantalla "Configuración" (solo lectura): muestra la configuración maestra vigente,
// el resultado de la última sincronización y permite sincronizar o cambiar la hoja.
// Todo se pinta con textContent/nodos DOM (nunca innerHTML): los textos vienen de una
// hoja de cálculo editable por terceros.
(function () {
    const MODAL_ID = 'configModal';
    let lastSync = null; // resultado de la última sincronización hecha desde esta pantalla

    // el('div', {class: '...'}, 'texto' | nodo | [hijos])
    function el(tag, attrs, children) {
        const node = document.createElement(tag);
        for (const [k, v] of Object.entries(attrs || {})) {
            if (k === 'class') node.className = v;
            else node.setAttribute(k, v);
        }
        for (const c of [].concat(children === undefined ? [] : children)) {
            node.append(c instanceof Node ? c : document.createTextNode(String(c)));
        }
        return node;
    }

    const LEVEL = {
        'error':       { label: 'Error',       cls: 'bg-red-100 text-red-800 border-red-200' },
        'advertencia': { label: 'Advertencia', cls: 'bg-amber-100 text-amber-800 border-amber-200' },
        'info':        { label: 'Info',        cls: 'bg-blue-100 text-blue-800 border-blue-200' },
    };
    const ORDER = { 'error': 0, 'advertencia': 1, 'info': 2 };

    function formatDate(iso) {
        const d = new Date(iso);
        return isNaN(d) ? '' : d.toLocaleString('es-CO', { dateStyle: 'medium', timeStyle: 'short' });
    }

    // Solo se enlazan hojas de Google (el servidor ya lo exige; aquí se evita un href peligroso).
    function safeSheetLink(url) {
        return typeof url === 'string' && url.startsWith('https://docs.google.com/') ? url : null;
    }

    function banner(kind, text) {
        const cls = {
            ok:    'bg-green-50 border-green-200 text-green-800',
            error: 'bg-red-50 border-red-200 text-red-800',
            warn:  'bg-amber-50 border-amber-200 text-amber-800',
        }[kind];
        return el('div', { class: `border rounded-lg p-3 text-sm font-medium ${cls}`, role: 'status' }, text);
    }

    function table(headers, rows) {
        const thead = el('thead', { class: 'bg-gray-50' }, el('tr', {},
            headers.map(h => el('th', { class: 'text-left text-xs font-semibold text-gray-500 uppercase px-3 py-2' }, h))));
        const tbody = el('tbody', {}, rows.map(r => el('tr', { class: 'border-t border-gray-100' },
            r.map(c => el('td', { class: 'px-3 py-2 text-sm' }, c)))));
        return el('div', { class: 'border border-gray-200 rounded-lg overflow-x-auto' },
            el('table', { class: 'w-full' }, [thead, tbody]));
    }

    function section(title, content) {
        return el('div', { class: 'mb-6' }, [el('h3', { class: 'font-semibold text-gray-700 mb-2' }, title), content]);
    }

    function issuesList(issues) {
        const sorted = [...issues].sort((a, b) => (ORDER[a.nivel] ?? 9) - (ORDER[b.nivel] ?? 9));
        return el('ul', { class: 'space-y-2' }, sorted.map(i => {
            const lv = LEVEL[i.nivel] || LEVEL.info;
            return el('li', { class: 'flex items-start gap-2 text-sm' }, [
                el('span', { class: `border rounded px-2 py-0.5 text-xs font-semibold shrink-0 ${lv.cls}` }, lv.label),
                el('span', {}, [el('span', { class: 'font-medium' }, i.campo + ': '), i.mensaje]),
            ]);
        }));
    }

    function render(cfg) {
        const body = document.getElementById('cfgBody');
        body.replaceChildren();

        // Estado + enlace a la hoja
        const link = safeSheetLink(cfg.enlace_hoja);
        const estado = el('div', { class: 'flex flex-wrap items-center gap-x-6 gap-y-1 text-sm text-gray-600 mb-4' }, [
            cfg.sincronizada
                ? el('span', {}, ['Versión ', el('code', { class: 'bg-gray-100 px-1.5 py-0.5 rounded text-xs' }, cfg.version),
                    ' · sincronizada ', formatDate(cfg.sincronizada_en)])
                : el('span', { class: 'text-amber-700 font-medium' }, 'Aún no hay una configuración sincronizada.'),
            link ? el('a', { href: link, target: '_blank', rel: 'noopener noreferrer',
                class: 'text-blue-600 hover:underline font-medium' }, 'Abrir la hoja en Google ↗') : '',
        ]);
        body.append(estado);

        // Resultado de la última sincronización (si se hizo desde aquí)
        if (lastSync) body.append(lastSync);

        // Formulario de sincronización
        const input = el('input', { type: 'url', id: 'cfgUrl', autocomplete: 'off', spellcheck: 'false',
            placeholder: 'Pega aquí una nueva URL publicada (CSV) o déjalo vacío para usar la hoja actual',
            class: 'flex-1 border border-gray-300 rounded-lg p-2.5 text-sm focus:ring-2 focus:ring-blue-500 focus:outline-none' });
        const btn = el('button', { type: 'button', id: 'cfgSyncBtn',
            class: 'bg-blue-600 hover:bg-blue-700 text-white font-bold py-2.5 px-5 rounded-lg text-sm whitespace-nowrap cursor-pointer' }, 'Sincronizar ahora');
        btn.addEventListener('click', () => runSync(input.value.trim(), btn));
        body.append(section('Sincronizar con Google Sheets', el('div', {}, [
            el('div', { class: 'flex flex-col sm:flex-row gap-2' }, [input, btn]),
            el('p', { class: 'text-xs text-gray-500 mt-1' },
                'En Google Sheets: Archivo → Compartir → Publicar en la web → formato CSV. Si cambias de hoja, la nueva URL se guarda solo cuando es válida.'),
        ])));

        body.append(templatesSection());

        if (!cfg.sincronizada) return;

        // Hallazgos
        if (cfg.hallazgos && cfg.hallazgos.length) {
            body.append(section(`Hallazgos de la última sincronización (${cfg.hallazgos.length})`, issuesList(cfg.hallazgos)));
        }
        // Becas
        body.append(section('Becas y planes de pago', table(
            ['Beca', 'Precio total', 'Pago # 1', 'Pago # 2', 'Pago # 3'],
            cfg.becas.map(b => [b.nombre, b.total, b.pagos[0] || '—', b.pagos[1] || '—', b.pagos[2] || '—']))));
        // Torneos
        body.append(section('Torneos por año de nacimiento', table(
            ['Años', 'Torneo'], cfg.torneos.map(t => [anios(t.anios), t.nombre]))));
        // Categorías
        body.append(section('Categorías por año de nacimiento', table(
            ['Años', 'Categoría', 'Plantilla'],
            cfg.categorias.map(c => [anios(c.anios), c.nombre, c.plantilla || '⚠ sin plantilla']))));
    }

    // ------------------------------------------------------------ plantillas Word
    // Las propuestas se generan rellenando estas plantillas. Al subir una nueva se valida que conserve los
    // campos XXXX/XX/XX/XXXX; si no, NO se cambia y se explica qué falta. La anterior queda guardada.
    function templatesSection() {
        const box = el('div', {}, [el('p', { class: 'text-sm text-gray-500' }, 'Cargando plantillas…')]);
        loadTemplates(box);
        return section('Plantillas Word de las propuestas', box);
    }

    async function loadTemplates(box, message) {
        const r = await listTemplates().catch(() => ({ ok: false, data: {} }));
        if (!r.ok) { box.replaceChildren(banner('error', 'No se pudieron leer las plantillas.')); return; }
        const kids = [];
        if (message) kids.push(message);
        r.data.templates.forEach(t => {
            const input = el('input', { type: 'file', accept: '.docx', class: 'text-xs flex-1 min-w-0' });
            const upload = el('button', { type: 'button', class: 'bg-blue-600 hover:bg-blue-700 text-white font-bold py-1.5 px-3 rounded-lg text-xs whitespace-nowrap cursor-pointer disabled:opacity-50' }, t.instalada ? 'Reemplazar' : 'Subir');
            upload.addEventListener('click', async () => {
                if (!input.files.length) { loadTemplates(box, banner('warn', `Elige primero el archivo .docx de «${t.nombre}».`)); return; }
                upload.disabled = true;
                const res = await installTemplate(t.clave, input.files[0]).catch(() => ({ ok: false, data: { error: 'No se pudo conectar con el servidor.' } }));
                loadTemplates(box, banner(res.ok ? 'ok' : 'error', res.ok ? res.data.message : (res.data.error || 'No se pudo subir la plantilla.')));
            });
            const status = t.instalada
                ? el('span', { class: 'text-green-700 text-xs font-semibold' }, `Instalada${t.versiones_anteriores ? ` · ${t.versiones_anteriores} versión(es) anterior(es) guardada(s)` : ''}`)
                : el('span', { class: 'text-red-600 text-xs font-semibold' }, 'FALTA: no se podrán generar propuestas de esta categoría');
            kids.push(el('div', { class: 'border border-gray-200 rounded-lg p-3 mb-2' }, [
                el('div', { class: 'flex flex-wrap items-center justify-between gap-2 mb-2' }, [el('span', { class: 'font-medium text-sm' }, t.nombre), status]),
                el('div', { class: 'flex items-center gap-2' }, [input, upload]),
            ]));
        });
        box.replaceChildren(...kids);
    }

    function anios(r) { return r.min === r.max ? String(r.min) : `${r.min}-${r.max}`; }

    async function load() {
        const body = document.getElementById('cfgBody');
        try {
            render(await getConfig());
        } catch (e) {
            body.replaceChildren(banner('error', e.message));
        }
    }

    async function runSync(url, btn) {
        const original = btn.textContent;
        btn.disabled = true;
        btn.textContent = 'Sincronizando…';
        lastSync = outcomeBanner(await syncConfig(url).catch(e => ({ ok: false, status: 0, data: { error: e.message } })));
        await load();
        const syncedUrl = document.getElementById('cfgUrl');
        if (syncedUrl) syncedUrl.focus();
        btn.disabled = false;
        btn.textContent = original;
    }

    // Convierte la respuesta de /config/sync en un bloque visible (incluye los errores de la hoja).
    function outcomeBanner(r) {
        const wrap = el('div', { class: 'mb-4 space-y-3' });
        if (r.ok) {
            const res = r.data.resultado || {};
            const s = res.resumen || {};
            wrap.append(banner('ok', `${res.cambio ? 'Configuración actualizada' : 'La hoja no tiene cambios'} · ` +
                `${s.becas} becas, ${s.torneos} torneos, ${s.categorias} categorías.`));
            const extra = (res.hallazgos || []).filter(i => i.nivel !== 'info');
            if (extra.length) wrap.append(issuesList(extra));
        } else if (r.status === 422) {
            const res = r.data.resultado || {};
            wrap.append(banner('error', `La hoja tiene ${(res.resumen || {}).errores || 'varios'} error(es): NO se aplicó y se conserva la configuración anterior. Corrígela en Google Sheets y vuelve a sincronizar.`));
            wrap.append(issuesList(res.hallazgos || []));
        } else {
            wrap.append(banner(r.status === 400 ? 'warn' : 'error', r.data.error || 'No se pudo sincronizar.'));
        }
        return wrap;
    }

    function onKey(e) { if (e.key === 'Escape') closeConfigPanel(); }

    window.openConfigPanel = function (syncResult) {
        const modal = document.getElementById(MODAL_ID);
        if (syncResult) lastSync = outcomeBanner(syncResult); // p. ej. al fallar el paso 1
        modal.classList.remove('step-hidden');
        modal.classList.add('step-flex');
        document.addEventListener('keydown', onKey);
        document.getElementById('cfgBody').replaceChildren(banner('warn', 'Cargando…'));
        load();
    };

    window.closeConfigPanel = function () {
        const modal = document.getElementById(MODAL_ID);
        modal.classList.add('step-hidden');
        modal.classList.remove('step-flex');
        document.removeEventListener('keydown', onKey);
        lastSync = null;
    };
})();
