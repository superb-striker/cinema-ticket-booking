package com.cinema.qa;

import com.cinema.qa.pages.AdminPage;
import com.cinema.qa.pages.CustomerPage;
import com.cinema.qa.pages.ScannerPage;
import com.cinema.qa.support.BrowserApi;
import com.cinema.qa.support.BrowserSession;
import com.cinema.qa.support.ConfirmedBookingFixture;
import com.cinema.qa.support.FailureArtifacts;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.RegisterExtension;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.CsvSource;
import org.junit.jupiter.params.provider.ValueSource;
import org.openqa.selenium.WebDriver;

import static org.junit.jupiter.api.Assertions.*;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

class CinemaUiTest {
    private static final String PASSWORD = "ValidPassword123!";
    private static String sharedEmail;
    private WebDriver driver;
    private CustomerPage customer;

    @RegisterExtension
    final FailureArtifacts failureArtifacts = new FailureArtifacts(() -> driver);

    @BeforeAll
    static void createSharedCustomer() throws Exception {
        sharedEmail = "qa-shared-" + UUID.randomUUID() + "@example.invalid";
        String body = "{\"email\":\"" + sharedEmail + "\",\"password\":\"" + PASSWORD + "\"}";
        HttpRequest request = HttpRequest.newBuilder(URI.create(BrowserSession.baseUrl() + "/api/v1/auth/register"))
                .header("Content-Type", "application/json")
                .POST(HttpRequest.BodyPublishers.ofString(body)).build();
        HttpResponse<String> response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString());
        assertEquals(201, response.statusCode(), "Could not create shared test account: " + response.body());
    }

    @BeforeEach
    void startBrowser() {
        driver = BrowserSession.create();
        customer = new CustomerPage(driver);
    }

    @AfterEach
    void closeBrowser() {
        if (driver != null) driver.quit();
    }

    private String newCustomer() {
        String email = "qa-" + UUID.randomUUID() + "@example.invalid";
        customer.open();
        customer.credentials(email, PASSWORD);
        customer.register();
        customer.awaitSignedIn(email);
        return email;
    }

    private void signInCustomer() {
        customer.open();
        customer.credentials(sharedEmail, PASSWORD);
        customer.signIn();
        customer.awaitSignedIn(sharedEmail);
    }

    @Test
    void customerCanRegisterSignOutAndLogInAgain() {
        String email = newCustomer();
        assertFalse(customer.adminLinkVisible());
        customer.signOut();
        customer.awaitSignedOut();
        customer.credentials(email, PASSWORD);
        customer.signIn();
        customer.awaitSignedIn(email);
        customer.awaitMessageContaining("Signed in");
    }

    @ParameterizedTest
    @CsvSource({
        "bad-email, ValidPassword123!",
        "qa-invalid@example.invalid, short"
    })
    void registrationRejectsInvalidCredentials(String email, String password) {
        customer.open();
        customer.credentials(email, password);
        customer.register();
        customer.awaitMessageContaining("valid email and a 12-72 character password");
        customer.awaitSignedOut();
    }

    @Test
    void loginRejectsWrongPassword() {
        customer.open();
        customer.credentials(sharedEmail, "WrongPassword123!");
        customer.signIn();
        customer.awaitMessageContaining("invalid email or password");
        customer.awaitSignedOut();
    }

    @Test
    void movieSearchFindsASeededScreening() {
        customer.open();
        customer.awaitScreenings();
        String title = customer.firstScreeningTitle();
        customer.filterMovie(title);
        customer.awaitScreenings();
        assertEquals(title, customer.firstScreeningTitle());
    }

    @ParameterizedTest
    @ValueSource(strings = {"movieFilter", "cityFilter", "languageFilter"})
    void searchShowsNoResultsForUnmatchedFilters(String filterId) {
        customer.open();
        customer.awaitScreenings();
        customer.filter(filterId, "qa-no-match-" + UUID.randomUUID());
        customer.awaitNoScreenings();
    }

    @Test
    void selectedSeatBecomesUnavailableAfterHoldAndSecondHoldIsRejected() {
        signInCustomer();
        customer.awaitScreenings();
        BrowserApi api = new BrowserApi(driver);
        List<Map<String, Object>> screenings = api.screenings();
        assertFalse(screenings.isEmpty(), "The test stack needs a future seeded screening");
        String screeningId = (String) screenings.get(0).get("id");
        customer.selectFirstScreening();
        String seatId = customer.firstAvailableSeat();
        customer.selectSeat(seatId);
        customer.awaitSelected(seatId);

        Map<String, Object> hold = api.hold(screeningId, seatId);
        assertEquals(201L, ((Number) hold.get("status")).longValue(), hold.toString());
        customer.awaitSeatUnavailable(seatId);
        assertEquals(409L, ((Number) api.hold(screeningId, seatId).get("status")).longValue());
    }

    @Test
    void customerCanNavigateAndRefreshBookingHistory() {
        signInCustomer();
        customer.awaitBookingHistory();
        assertTrue(customer.bookingListText().contains("No bookings yet"));
        customer.refreshBookings();
        assertTrue(customer.bookingListText().contains("No bookings yet"));
    }

    @Test
    void confirmedBookingAppearsInHistoryWithTicket() throws Exception {
        try (ConfirmedBookingFixture fixture = ConfirmedBookingFixture.create(sharedEmail)) {
            customer.open();
            customer.credentials(fixture.email(), PASSWORD);
            customer.signIn();
            customer.awaitSignedIn(fixture.email());
            customer.awaitConfirmedBooking(fixture.movieTitle(), fixture.seatId());
            assertEquals(200L, ((Number) new BrowserApi(driver)
                    .request("/api/v1/bookings/" + fixture.orderId() + "/ticket", "GET", null)
                    .get("status")).longValue());
        }
    }

    @Test
    void customerIsDeniedAdminPageAndApi() {
        signInCustomer();
        assertFalse(customer.adminLinkVisible());
        AdminPage admin = new AdminPage(driver).open();
        admin.awaitAccessDenied();
        assertTrue(admin.denialText().contains("Administrator access required"));
        assertEquals(403L, ((Number) new BrowserApi(driver)
                .request("/api/v1/admin/movies", "GET", null).get("status")).longValue());
    }

    @Test
    void scannerRequiresKeyAndRejectsUnauthorizedSubmission() {
        ScannerPage scanner = new ScannerPage(driver).open();
        scanner.enterCode("invalid-ticket-code");
        scanner.submit();
        scanner.awaitResult("Scanner key required");
        scanner.enterKey("wrong-scanner-key");
        scanner.submit();
        scanner.awaitResult("Admission denied");
        assertTrue(scanner.resultText().contains("scanner authentication required"));
    }

    @Test
    void scannerRejectsMalformedTicketWithAuthorizedKey() {
        String key = System.getProperty("scannerKey", "");
        assumeTrue(!key.isBlank(), "Set -DscannerKey to exercise authenticated scanner validation");
        ScannerPage scanner = new ScannerPage(driver).open();
        scanner.enterKey(key);
        scanner.enterCode("invalid-ticket-code");
        scanner.submit();
        scanner.awaitResult("Admission denied");
        assertTrue(scanner.resultText().contains("ticket is invalid"));
    }

    @Test
    void scannerApprovesTicketOnceAndRejectsDuplicate() throws Exception {
        String key = System.getProperty("scannerKey", "");
        assumeTrue(!key.isBlank(), "Set -DscannerKey to exercise authenticated scanner validation");
        try (ConfirmedBookingFixture fixture = ConfirmedBookingFixture.create(sharedEmail)) {
            ScannerPage scanner = new ScannerPage(driver).open();
            scanner.enterKey(key);
            scanner.enterCode(fixture.ticketCode());
            scanner.submit();
            scanner.awaitResult("Admission approved");
            assertTrue(scanner.resultText().contains(fixture.orderId()));
            scanner.submit();
            scanner.awaitResult("Admission denied");
            assertTrue(scanner.resultText().contains("ticket has already been admitted"));
        }
    }
}
