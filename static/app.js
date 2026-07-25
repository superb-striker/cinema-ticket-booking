(() => {
  const state = {
    user: null,
    screening: null,
    seats: [],
    selected: new Set(),
    seatEvents: null,
  };
  const $ = (id) => document.getElementById(id);
  async function api(path, options = {}) {
    options.headers = {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    };
    const response = await fetch(path, options);
    if (response.status === 204) return null;
    const data = await response
      .json()
      .catch(() => ({ error: "Invalid server response" }));
    if (!response.ok) throw new Error(data.error || "Request failed");
    return data;
  }
  function message(text, type = "") {
    const el = $("message");
    el.textContent = text;
    el.className = type;
  }
  async function loadIdentity() {
    try {
      state.user = await api("/api/v1/auth/me");
      $("identity").textContent = state.user.email;
      $("auth").classList.add("hidden");
    } catch {
      state.user = null;
    }
  }
  $("authForm").addEventListener("click", async (event) => {
    if (event.target.tagName !== "BUTTON") return;
    event.preventDefault();
    try {
      state.user = await api(`/api/v1/auth/${event.target.dataset.action}`, {
        method: "POST",
        body: JSON.stringify({
          email: $("email").value,
          password: $("password").value,
        }),
      });
      $("identity").textContent = state.user.email;
      $("auth").classList.add("hidden");
      message("Signed in", "success");
    } catch (error) {
      message(error.message, "error");
    }
  });
  let screeningRequest = 0;
  async function loadScreenings() {
    const request = ++screeningRequest;
    const root = $("screenings");
    root.setAttribute("aria-busy", "true");
    try {
      const params = new URLSearchParams();
      const filters = {
        q: $("movieFilter").value,
        city: $("cityFilter").value,
        date: $("dateFilter").value,
        language: $("languageFilter").value,
        format: $("formatFilter").value,
      };
      Object.entries(filters).forEach(([key, value]) => {
        if (value.trim()) params.set(key, value.trim());
      });
      const items = await api(`/api/v1/screenings?${params}`);
      if (request !== screeningRequest) return;
      root.innerHTML = "";
      if (!items.length) {
        root.innerHTML = '<p class="muted">No screenings match these filters.</p>';
      }
      items.forEach((item) => {
        const button = document.createElement("button");
        button.className = "screening";
        button.innerHTML = `<strong>${escapeHTML(item.movie_title)}</strong><br><span class="muted">${new Date(item.starts_at).toLocaleString()} · ${escapeHTML(item.cinema_name)}, ${escapeHTML(item.cinema_city)} · ${escapeHTML(item.screen_name)} · ${escapeHTML(item.format)} · ₹${(item.base_price_paise / 100).toFixed(2)}</span>`;
        button.onclick = () => selectScreening(item, button);
        root.append(button);
      });
    } catch (error) {
      if (request !== screeningRequest) return;
      message(error.message, "error");
    } finally {
      root.setAttribute("aria-busy", "false");
    }
  }
  let filterTimer;
  $("filters").addEventListener("input", () => {
    clearTimeout(filterTimer);
    filterTimer = setTimeout(loadScreenings, 250);
  });
  $("filters").addEventListener("reset", () => setTimeout(loadScreenings));
  $("filters").addEventListener("submit", (event) => {
    event.preventDefault();
    clearTimeout(filterTimer);
    loadScreenings();
  });
  async function selectScreening(item, button) {
    state.screening = item;
    state.selected.clear();
    document
      .querySelectorAll(".screening")
      .forEach((x) => x.classList.remove("selected"));
    button.classList.add("selected");
    $("booking").classList.remove("hidden");
    $("screeningTitle").textContent = item.movie_title;
    await loadSeats();
    subscribeToSeats(item.id);
  }
  function subscribeToSeats(screeningID) {
    if (state.seatEvents) state.seatEvents.close();
    const source = new EventSource(`/api/v1/screenings/${screeningID}/seat-events`);
    state.seatEvents = source;
    source.addEventListener("seats", (event) => {
      if (state.screening?.id !== screeningID) return;
      state.seats = JSON.parse(event.data);
      for (const id of [...state.selected]) {
        if (state.seats.find((seat) => seat.id === id)?.status !== "available") {
          state.selected.delete(id);
        }
      }
      renderSeats();
    });
    source.onerror = () => message("Seat updates interrupted; reconnecting…", "error");
  }
  async function loadSeats() {
    try {
      state.seats = await api(`/api/v1/screenings/${state.screening.id}/seats`);
      renderSeats();
    } catch (error) {
      message(error.message, "error");
    }
  }
  function renderSeats() {
    const root = $("seats");
    root.innerHTML = "";
    state.seats.forEach((seat) => {
      const button = document.createElement("button");
      button.textContent = seat.id;
      button.className = `seat ${seat.status}`;
      button.disabled = seat.status !== "available";
      button.type = "button";
      button.setAttribute("aria-label", `Seat ${seat.id}: ${seat.status.replaceAll("_", " ")}`);
      button.setAttribute("aria-pressed", state.selected.has(seat.id) ? "true" : "false");
      if (state.selected.has(seat.id)) button.classList.add("selected");
      button.onclick = () => {
        state.selected.has(seat.id)
          ? state.selected.delete(seat.id)
          : state.selected.add(seat.id);
        renderSeats();
      };
      root.append(button);
    });
    const count = state.selected.size;
    $("selection").textContent = count
      ? `${[...state.selected].join(", ")} (${count} seat${count > 1 ? "s" : ""})`
      : "No seats selected";
    $("price").textContent = count
      ? `Total ₹${((count * state.screening.base_price_paise) / 100).toFixed(2)}`
      : "";
    $("checkout").disabled = !count;
  }
  $("checkout").onclick = async () => {
    if (!state.user) {
      message("Sign in before booking", "error");
      return;
    }
    $("checkout").disabled = true;
    try {
      const hold = await api(`/api/v1/screenings/${state.screening.id}/holds`, {
        method: "POST",
        body: JSON.stringify({ seat_ids: [...state.selected] }),
      });
      const result = await api(
        `/api/v1/screenings/${state.screening.id}/holds/${hold.id}/checkout`,
        { method: "POST", headers: { "Idempotency-Key": crypto.randomUUID() } },
      );
      openPayment(result);
    } catch (error) {
      message(error.message, "error");
      await loadSeats();
    }
  };
  function openPayment(result) {
    const order = result.order;
    const instance = new Razorpay({
      key: result.checkout_key,
      amount: order.amount_paise,
      currency: order.currency,
      order_id: order.provider_order_id,
      name: "Cinema Booking",
      description: `Seats ${order.seat_ids.join(", ")}`,
      prefill: { email: state.user.email },
      handler: async (response) => {
        try {
          await api(`/api/v1/orders/${order.id}/verify-payment`, {
            method: "POST",
            body: JSON.stringify(response),
          });
          message("Payment received. Confirming your ticket…", "success");
          pollOrder(order.id);
        } catch (error) {
          message(error.message, "error");
        }
      },
      modal: {
        ondismiss: () =>
          message(
            "Payment window closed. Your hold will expire automatically.",
          ),
      },
      theme: { color: "#65d1ff" },
    });
    instance.open();
  }
  async function pollOrder(id) {
    for (let i = 0; i < 15; i++) {
      await new Promise((resolve) => setTimeout(resolve, 2000));
      const order = await api(`/api/v1/orders/${id}`);
      if (order.status === "confirmed") {
        message(`Booking confirmed: ${order.seat_ids.join(", ")}`, "success");
        state.selected.clear();
        await loadSeats();
        return;
      }
      if (
        ["payment_failed", "expired", "refund_pending", "refunded"].includes(
          order.status,
        )
      ) {
        message(`Payment status: ${order.status}`, "error");
        return;
      }
    }
    message(
      "Payment is still processing. Your order remains available in the API.",
    );
  }
  function escapeHTML(value) {
    const div = document.createElement("div");
    div.textContent = value;
    return div.innerHTML;
  }
  Promise.all([loadIdentity(), loadScreenings()]);
})();
