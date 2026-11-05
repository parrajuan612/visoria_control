// Tabla editable de revisión (paso 2): muestra las filas del Excel ya validadas por el servidor,
// resalta las celdas con problemas y permite corregirlas en pantalla sin volver a subir el archivo.
// El servidor es la fuente de verdad: cada corrección se envía (PATCH), se revalida allá y la
// fila se redibuja con el resultado. Todo se pinta con nodos DOM/textContent (nunca innerHTML).
(function () {
    const COLS = [
        { key: 'jugador', label: 'Jugador', width: 'min-w-[9rem]' },
        { key: 'club', label: 'Club', width: 'min-w-[6rem]' },
        { key: 'beca', label: 'Beca', width: 'min-w-[3.5rem]' },
        { key: 'anio', label: 'Año', width: 'min-w-[3.5rem]' },
        { key: 'acudiente', label: 'Acudiente', width: 'min-w-[8rem]' },
        { key: 'movil', label: 'Móvil', width: 'min-w-[7rem]' },
        { key: 'correo', label: 'Correo', width: 'min-w-[9rem]' },
    ];
    // A qué columna(s) pertenece cada hallazgo del servidor.
    const CAMPO_A_COLUMNAS = {
        'NOMBRE DEL JUGADOR': ['jugador'], 'CLUB': ['club'], 'BECA': ['beca'], 'AÑO DE NACIMIENTO': ['anio'],
        'Categoría': ['anio'], 'Torneo': ['anio'], 'NOMBRE DEL ACUDIENTE': ['acudiente'],
        'MOVIL': ['movil'], 'CORREO ELECTRONICO': ['correo'], 'Canales de envío': ['movil', 'correo'],
    };
    const FILTERS = [
        { id: 'all', label: 'Todas', test: () => true },
        { id: 'errors', label: 'Con errores', test: r => r.estado === 'INVALID' },
        { id: 'warnings', label: 'Con advertencias', test: r => hasLevel(r, 'advertencia') },
        { id: 'omitted', label: 'Sin beca', test: r => r.estado === 'OMITTED' },
    ];
    const LEVEL_CLASS = { error: 'text-red-600', advertencia: 'text-amber-600', info: 'text-gray-500' };

    const state = { campaign: null, rows: [], filter: 'all', pinned: new Set(), saving: false };

    function node(tag, className, text) {
        const n = document.createElement(tag);
        if (className) n.className = className;
        if (text !== undefined) n.textContent = text;
        return n;
    }
    const hasLevel = (r, lvl) => (r.issues || []).some(i => i.nivel === lvl);

    // Gravedad de una celda: la peor entre los hallazgos que apuntan a su columna.
    function cellSeverity(rec, col) {
        let sev = null;
        for (const i of rec.issues || []) {
            if (!(CAMPO_A_COLUMNAS[i.campo] || []).includes(col)) continue;
            if (i.nivel === 'error') return 'error';
            if (i.nivel === 'advertencia') sev = 'advertencia';
        }
        return sev;
    }
    function cellMessages(rec, col) {
        return (rec.issues || []).filter(i => (CAMPO_A_COLUMNAS[i.campo] || []).includes(col) && i.nivel !== 'info')
            .map(i => i.mensaje).join(' · ');
    }

    // ---------------------------------------------------------------- resumen y filtros
    function counts() {
        const c = { all: state.rows.length };
        for (const f of FILTERS) c[f.id] = state.rows.filter(f.test).length;
        return c;
    }

    function renderSummary(resumen) {
        const box = document.getElementById('validationSummary');
        box.classList.remove('hidden');
        box.replaceChildren(
            node('span', 'text-green-700 mr-4', `${resumen.validas} listas`),
            node('span', 'text-red-700 mr-4', `${resumen.con_errores} con errores`),
            node('span', 'text-gray-500 mr-4', `${resumen.omitidas} sin beca (se omiten)`),
            node('span', 'text-amber-700', `${resumen.con_advertencias} con advertencias`));
    }

    function renderFilters() {
        const bar = document.getElementById('validationFilters');
        bar.classList.remove('hidden');
        bar.classList.add('flex');
        const c = counts();
        bar.replaceChildren(...FILTERS.map(f => {
            const active = state.filter === f.id;
            const b = node('button', `px-3 py-1.5 rounded-full text-xs font-semibold border cursor-pointer ${active
                ? 'bg-blue-600 text-white border-blue-600' : 'bg-white text-gray-600 border-gray-300 hover:bg-gray-50'}`,
                `${f.label} (${c[f.id]})`);
            b.type = 'button';
            b.setAttribute('aria-pressed', String(active));
            b.addEventListener('click', () => { state.filter = f.id; state.pinned.clear(); renderTable(); renderFilters(); });
            return b;
        }));
    }

    // ---------------------------------------------------------------- tabla
    // Estado compacto: una etiqueta y dos iconos de canal (con tooltip). Va en la 2ª columna para
    // que se vea sin desplazarse horizontalmente.
    function statusBadge(rec) {
        const wrap = node('div', 'space-y-0.5');
        const map = {
            VALID: [hasLevel(rec, 'advertencia') ? 'Lista ⚠' : 'Lista', hasLevel(rec, 'advertencia') ? 'text-amber-700' : 'text-green-600'],
            INVALID: ['Errores', 'text-red-600'],
            OMITTED: ['Sin beca', 'text-gray-500'],
        };
        const [label, cls] = map[rec.estado] || [rec.estado, 'text-gray-500'];
        wrap.append(node('div', `text-xs font-bold ${cls}`, label));
        if (rec.estado !== 'OMITTED') {
            const ch = node('div', 'text-xs whitespace-nowrap');
            const wa = node('span', rec.movil ? '' : 'opacity-25', '💬');
            wa.title = rec.movil ? 'Recibirá WhatsApp' : 'Sin WhatsApp: no tiene un móvil válido';
            const mail = node('span', (rec.correos || []).length ? 'ml-1' : 'ml-1 opacity-25', '✉');
            mail.title = (rec.correos || []).length ? 'Recibirá correo' : 'Sin correo: no tiene un correo válido';
            ch.append(wa, mail);
            if (rec.editado) ch.append(node('span', 'ml-1 text-blue-600', '✎'));
            if (rec.editado) ch.title = 'Corregida en pantalla';
            wrap.append(ch);
        }
        return wrap;
    }

    function dataCell(rec, col) {
        const td = node('td', 'p-0 align-top');
        const sev = cellSeverity(rec, col.key);
        const value = (rec.raw && rec.raw[col.key]) || '';
        const btn = node('button', `w-full text-left p-2 text-sm leading-snug cursor-text hover:bg-blue-50 focus:outline-none focus:ring-2 focus:ring-blue-400 ${col.width} ${
            sev === 'error' ? 'bg-red-100 text-red-800' : sev === 'advertencia' ? 'bg-amber-50 text-amber-800' : ''}`);
        btn.type = 'button';
        btn.textContent = value === '' ? '—' : value;
        if (value === '') btn.classList.add('text-gray-400');
        const msg = cellMessages(rec, col.key);
        btn.title = msg ? `${msg} (clic para corregir)` : 'Clic para corregir';
        btn.setAttribute('aria-label', `${col.label} de la fila ${rec.fila}: ${value || 'vacío'}. Editar`);
        btn.addEventListener('click', () => startEdit(td, rec, col, value));
        td.append(btn);
        return td;
    }

    function rowNodes(rec) {
        const bg = rec.estado === 'INVALID' ? 'bg-red-50/40' : rec.estado === 'OMITTED' ? 'bg-gray-50 text-gray-500' : '';
        const tr = node('tr', `border-t border-gray-100 ${bg}`);
        tr.dataset.rid = rec.id;
        tr.append(node('td', 'p-2 text-xs text-gray-400 align-top', String(rec.fila)));
        const st = node('td', 'p-1.5 align-top');
        st.append(statusBadge(rec));
        tr.append(st);
        for (const col of COLS) tr.append(dataCell(rec, col));
        const out = [tr];

        const shown = (rec.issues || []).filter(i => i.nivel !== 'info');
        if (shown.length) {
            const ir = node('tr', `${bg}`);
            ir.dataset.rid = rec.id;
            const td = node('td', 'px-2 pb-2 pt-0 text-xs');
            td.colSpan = COLS.length + 2;
            const ul = node('ul', 'space-y-0.5');
            shown.slice().sort((a, b) => (a.nivel === 'error' ? 0 : 1) - (b.nivel === 'error' ? 0 : 1)).forEach(i =>
                ul.append(node('li', LEVEL_CLASS[i.nivel] || '', `${i.campo}: ${i.mensaje}`)));
            td.append(ul);
            ir.append(td);
            out.push(ir);
        }
        return out;
    }

    function renderTable() {
        const container = document.getElementById('validationTable');
        container.classList.remove('hidden');
        const filter = FILTERS.find(f => f.id === state.filter) || FILTERS[0];
        const visible = state.rows.filter(r => filter.test(r) || state.pinned.has(r.id));

        const head = node('tr');
        head.append(node('th', 'p-2 text-xs text-gray-400 font-normal', 'Fila'));
        head.append(node('th', 'p-1.5 text-left text-xs font-semibold text-gray-600', 'Estado'));
        COLS.forEach(c => head.append(node('th', 'p-2 text-left text-xs font-semibold text-gray-600', c.label)));
        const thead = node('thead', 'bg-gray-100');
        thead.append(head);
        const tbody = node('tbody');
        if (!visible.length) {
            const td = node('td', 'p-6 text-center text-sm text-gray-500', 'No hay filas en este filtro.');
            td.colSpan = COLS.length + 2;
            const tr = node('tr');
            tr.append(td);
            tbody.append(tr);
        }
        visible.forEach(r => tbody.append(...rowNodes(r)));
        const table = node('table', 'w-full text-left bg-white rounded shadow text-sm');
        table.append(thead, tbody);

        const scroll = container.scrollTop;
        container.replaceChildren(table);
        container.scrollTop = scroll;
    }

    // ---------------------------------------------------------------- edición en la celda
    function toast(msg, kind) {
        const box = document.getElementById('validationToast');
        box.textContent = msg;
        box.className = `mb-3 text-sm font-medium rounded-lg p-2 border ${kind === 'error'
            ? 'bg-red-50 border-red-200 text-red-700' : 'bg-green-50 border-green-200 text-green-700'}`;
        clearTimeout(toast.t);
        if (kind !== 'error') toast.t = setTimeout(() => box.classList.add('hidden'), 3000);
    }

    function startEdit(td, rec, col, current) {
        if (state.saving || td.querySelector('input')) return;
        const input = node('input', `w-full p-2 text-sm border-2 border-blue-500 rounded focus:outline-none ${col.width}`);
        input.type = 'text';
        input.value = current;
        input.maxLength = 300;
        input.setAttribute('aria-label', `Editar ${col.label} de la fila ${rec.fila}`);
        let done = false;
        const finish = async (save) => {
            if (done) return;
            done = true;
            const value = input.value.trim();
            if (!save || value === current.trim()) { renderTable(); return; }
            await commit(rec, col, value, input);
        };
        input.addEventListener('keydown', e => {
            if (e.key === 'Enter') { e.preventDefault(); finish(true); }
            else if (e.key === 'Escape') { e.preventDefault(); finish(false); }
        });
        input.addEventListener('blur', () => finish(true));
        td.replaceChildren(input);
        input.focus();
        input.select();
    }

    async function commit(rec, col, value, input) {
        state.saving = true;
        input.disabled = true;
        state.pinned.add(rec.id); // la fila no desaparece del filtro actual mientras se corrige
        let result;
        try {
            result = await patchRecipient(state.campaign.id, rec.id, { [col.key]: value });
        } catch (e) {
            result = { ok: false, data: { error: 'No se pudo conectar con el servidor.' } };
        }
        state.saving = false;
        if (!result.ok) {
            toast(result.data.error || 'No se pudo guardar la corrección.', 'error');
            renderTable();
            return;
        }
        const { recipient, otros_cambios, resumen } = result.data;
        for (const upd of [recipient, ...(otros_cambios || [])]) {
            const i = state.rows.findIndex(r => r.id === upd.id);
            if (i >= 0) state.rows[i] = upd;
        }
        state.campaign.resumen = resumen;
        renderSummary(resumen);
        renderFilters();
        renderTable();
        const extra = (otros_cambios || []).length ? ` (también cambió ${otros_cambios.length} fila(s) relacionada(s))` : '';
        toast(`Fila ${recipient.fila} actualizada: ${recipient.estado === 'VALID' ? 'lista' : recipient.estado === 'OMITTED' ? 'sin beca' : 'aún tiene errores'}${extra}.`,
            recipient.estado === 'INVALID' ? 'error' : 'ok');
    }

    // ---------------------------------------------------------------- API pública
    window.campaignView = {
        // Muestra la campaña recién creada (respuesta de POST /campaigns).
        show(detail) {
            state.campaign = detail.campaign;
            state.rows = detail.recipients;
            state.filter = state.rows.some(r => r.estado === 'INVALID') ? 'errors' : 'all'; // primero lo que hay que corregir
            state.pinned.clear();
            document.getElementById('validationToast').classList.add('hidden');
            renderSummary(detail.campaign.resumen);
            renderFilters();
            renderTable();
        },
        current() { return state.campaign; },
        rows() { return state.rows; },
    };
})();
