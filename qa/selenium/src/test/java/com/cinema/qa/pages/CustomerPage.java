package com.cinema.qa.pages;

import com.cinema.qa.support.BrowserSession;
import org.openqa.selenium.By;
import org.openqa.selenium.WebDriver;
import org.openqa.selenium.WebElement;

public final class CustomerPage extends BasePage {
    private static final By IDENTITY = By.id("identity");
    private static final By MESSAGE = By.id("message");
    private static final By SCREENING = By.cssSelector("#screenings button.screening");

    public CustomerPage(WebDriver driver) { super(driver); }

    public CustomerPage open() {
        driver.get(BrowserSession.baseUrl() + "/");
        visible(By.id("authForm"));
        return this;
    }

    public void credentials(String email, String password) {
        set(By.id("email"), email);
        set(By.id("password"), password);
    }

    public void register() { clickable(By.cssSelector("#authForm [data-action='register']")).click(); }
    public void signIn() { clickable(By.cssSelector("#authForm [data-action='login']")).click(); }
    public void signOut() { clickable(By.id("logout")).click(); }

    public void awaitSignedIn(String email) {
        wait.until(d -> d.findElement(IDENTITY).getText().equals(email));
        visible(By.id("bookings"));
    }

    public void awaitSignedOut() {
        wait.until(d -> d.findElement(IDENTITY).getText().equals("Not signed in"));
        visible(By.id("authForm"));
    }

    public void awaitMessageContaining(String expected) {
        wait.until(d -> d.findElement(MESSAGE).getText().contains(expected));
    }

    public String message() { return driver.findElement(MESSAGE).getText(); }

    public void awaitScreenings() {
        wait.until(d -> d.findElement(By.id("screenings")).getAttribute("aria-busy").equals("false"));
        visible(SCREENING);
    }

    public String firstScreeningTitle() {
        return visible(SCREENING).findElement(By.tagName("strong")).getText();
    }

    public void filterMovie(String query) {
        set(By.id("movieFilter"), query);
        clickable(By.cssSelector("#filters button[type='submit']")).click();
    }

    public void filter(String field, String query) {
        set(By.id(field), query);
        clickable(By.cssSelector("#filters button[type='submit']")).click();
    }

    public void awaitNoScreenings() {
        visible(By.xpath("//*[@id='screenings' and @aria-busy='false']//*[contains(text(),'No screenings match')]") );
    }

    public void selectFirstScreening() {
        clickable(SCREENING).click();
        visible(By.id("seats"));
        wait.until(d -> !d.findElements(By.cssSelector("#seats button.seat")).isEmpty());
    }

    public String firstAvailableSeat() {
        return clickable(By.cssSelector("#seats button.seat.available")).getText();
    }

    public void selectSeat(String seatId) {
        clickable(By.cssSelector("#seats button.seat[aria-label='Seat " + seatId + ": available']")).click();
    }

    public void awaitSelected(String seatId) {
        wait.until(d -> d.findElement(By.id("selection")).getText().contains(seatId));
        wait.until(d -> d.findElement(By.id("checkout")).isEnabled());
    }

    public void awaitSeatUnavailable(String seatId) {
        wait.until(d -> {
            WebElement seat = d.findElement(By.cssSelector("#seats button.seat[aria-label^='Seat " + seatId + ":']"));
            return !seat.isEnabled() && seat.getAttribute("aria-label").contains("held");
        });
        wait.until(d -> d.findElement(By.id("selection")).getText().equals("No seats selected"));
    }

    public void awaitBookingHistory() {
        visible(By.id("bookings"));
        wait.until(d -> d.findElement(By.id("bookingList")).getAttribute("aria-busy").equals("false"));
    }

    public String bookingListText() { return driver.findElement(By.id("bookingList")).getText(); }

    public void awaitConfirmedBooking(String movieTitle, String seatId) {
        wait.until(d -> d.findElements(By.cssSelector("#bookingList .booking-card")).stream()
                .anyMatch(card -> card.getText().contains(movieTitle)
                        && card.getText().contains(seatId)
                        && card.getText().contains("confirmed")
                        && !card.findElements(By.linkText("Download ticket")).isEmpty()));
    }

    public void refreshBookings() {
        clickable(By.id("refreshBookings")).click();
        awaitBookingHistory();
    }

    public boolean adminLinkVisible() { return driver.findElement(By.id("adminLink")).isDisplayed(); }
}
