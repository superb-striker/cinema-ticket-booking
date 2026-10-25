package com.cinema.qa.support;

import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.sql.Connection;
import java.sql.DriverManager;
import java.sql.PreparedStatement;
import java.sql.ResultSet;
import java.sql.SQLException;
import java.util.Base64;
import java.util.UUID;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/** A disposable confirmed order for downstream UI tests; never calls a payment provider. */
public final class ConfirmedBookingFixture implements AutoCloseable {
    private final UUID userId = UUID.randomUUID();
    private final UUID screeningId = UUID.randomUUID();
    private final UUID orderId = UUID.randomUUID();
    private final String email = "qa-confirmed-" + UUID.randomUUID() + "@example.invalid";
    private final String movieTitle;
    private final String seatId;
    private final String ticketCode;

    private ConfirmedBookingFixture(String sourceEmail) throws SQLException, GeneralSecurityException {
        try (Connection db = connect()) {
            db.setAutoCommit(false);
            try {
                String passwordHash;
                UUID movieId;
                UUID screenId;
                try (PreparedStatement statement = db.prepareStatement("SELECT password_hash FROM users WHERE email=?")) {
                    statement.setString(1, sourceEmail);
                    try (ResultSet rows = statement.executeQuery()) {
                        if (!rows.next()) throw new SQLException("Shared QA account was not found");
                        passwordHash = rows.getString(1);
                    }
                }
                try (PreparedStatement statement = db.prepareStatement("""
                        SELECT sc.movie_id, sc.screen_id, m.title, st.id
                        FROM screenings sc JOIN movies m ON m.id=sc.movie_id
                        JOIN seats st ON st.screen_id=sc.screen_id
                        WHERE sc.status='scheduled' AND sc.sales_end_at>now()
                        ORDER BY sc.starts_at, st.row_label, st.seat_number LIMIT 1
                        """); ResultSet rows = statement.executeQuery()) {
                    if (!rows.next()) throw new SQLException("A future seeded screening with seats is required");
                    movieId = rows.getObject(1, UUID.class);
                    screenId = rows.getObject(2, UUID.class);
                    movieTitle = rows.getString(3);
                    seatId = rows.getString(4);
                }
                execute(db, "INSERT INTO users(id,email,password_hash) VALUES(?,?,?)", userId, email, passwordHash);
                execute(db, """
                        INSERT INTO screenings(id,movie_id,screen_id,starts_at,sales_end_at,base_price_paise,currency)
                        VALUES(?,?,?,now()+interval '30 minutes',now()+interval '20 minutes',35000,'INR')
                        """, screeningId, movieId, screenId);
                execute(db, """
                        INSERT INTO orders(id,user_id,screening_id,hold_id,idempotency_key,status,
                                           amount_paise,currency,provider,expires_at,subtotal_paise)
                        VALUES(?,?,?,?,?,'confirmed',35000,'INR','qa-fixture',now()+interval '15 minutes',35000)
                        """, orderId, userId, screeningId, UUID.randomUUID(), "qa-" + UUID.randomUUID());
                execute(db, """
                        INSERT INTO order_seats(order_id,screening_id,seat_id,status,price_paise)
                        VALUES(?,?,?,'confirmed',35000)
                        """, orderId, screeningId, seatId);
                db.commit();
            } catch (SQLException error) {
                db.rollback();
                throw error;
            }
        }
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(System.getProperty("ticketSigningSecret",
                "local-ticket-signing-secret-change-in-production").getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
        ticketCode = orderId + "." + Base64.getUrlEncoder().withoutPadding()
                .encodeToString(mac.doFinal(orderId.toString().getBytes(StandardCharsets.UTF_8)));
    }

    public static ConfirmedBookingFixture create(String sourceEmail) throws SQLException, GeneralSecurityException {
        return new ConfirmedBookingFixture(sourceEmail);
    }

    private static Connection connect() throws SQLException {
        return DriverManager.getConnection(System.getProperty("fixture.jdbcUrl", "jdbc:postgresql://localhost:5433/cinema"),
                System.getProperty("fixture.dbUser", "cinema"),
                System.getProperty("fixture.dbPassword", "cinema-local-only"));
    }

    private static void execute(Connection db, String sql, Object... parameters) throws SQLException {
        try (PreparedStatement statement = db.prepareStatement(sql)) {
            for (int i = 0; i < parameters.length; i++) statement.setObject(i + 1, parameters[i]);
            statement.executeUpdate();
        }
    }

    public String email() { return email; }
    public String movieTitle() { return movieTitle; }
    public String seatId() { return seatId; }
    public String orderId() { return orderId.toString(); }
    public String ticketCode() { return ticketCode; }

    @Override
    public void close() throws SQLException {
        try (Connection db = connect()) {
            db.setAutoCommit(false);
            try {
                execute(db, "DELETE FROM ticket_scans WHERE order_id=?", orderId);
                execute(db, "DELETE FROM order_seats WHERE order_id=?", orderId);
                execute(db, "DELETE FROM orders WHERE id=?", orderId);
                execute(db, "DELETE FROM screenings WHERE id=?", screeningId);
                execute(db, "DELETE FROM users WHERE id=?", userId);
                db.commit();
            } catch (SQLException error) {
                db.rollback();
                throw error;
            }
        }
    }
}
