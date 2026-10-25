package com.cinema.qa.support;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.function.Supplier;
import org.junit.jupiter.api.extension.AfterTestExecutionCallback;
import org.junit.jupiter.api.extension.ExtensionContext;
import org.openqa.selenium.OutputType;
import org.openqa.selenium.TakesScreenshot;
import org.openqa.selenium.WebDriver;
import org.openqa.selenium.logging.LogType;

public final class FailureArtifacts implements AfterTestExecutionCallback {
    private final Supplier<WebDriver> driverSupplier;

    public FailureArtifacts(Supplier<WebDriver> driverSupplier) {
        this.driverSupplier = driverSupplier;
    }

    @Override
    public void afterTestExecution(ExtensionContext context) {
        WebDriver driver = driverSupplier.get();
        if (context.getExecutionException().isEmpty() || driver == null) return;
        Path directory = Path.of(System.getProperty("artifactsDir", "target/failure-artifacts"));
        String name = context.getRequiredTestClass().getSimpleName() + "-"
                + context.getRequiredTestMethod().getName() + "-" + Instant.now().toEpochMilli();
        try {
            Files.createDirectories(directory);
            Files.write(directory.resolve(name + ".png"),
                    ((TakesScreenshot) driver).getScreenshotAs(OutputType.BYTES));
        } catch (Exception error) {
            System.err.println("Could not capture screenshot: " + error);
        }
        try {
            StringBuilder output = new StringBuilder();
            driver.manage().logs().get(LogType.BROWSER).forEach(entry ->
                    output.append(entry.getLevel()).append(' ').append(entry.getMessage()).append('\n'));
            Files.writeString(directory.resolve(name + ".browser.log"), output.toString(), StandardCharsets.UTF_8);
        } catch (Exception error) {
            System.err.println("Could not capture browser logs: " + error);
        }
        System.err.println("Failure artifacts: " + directory.toAbsolutePath());
    }
}
