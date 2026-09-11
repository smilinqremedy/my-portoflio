document.addEventListener("DOMContentLoaded", function () {
    const checkInInput = document.getElementById("check-in");
    const checkOutInput = document.getElementById("check-out");
    const roomSelect = document.getElementById("room");
    const guestSelect = document.getElementById("guests");
    const bookingForm = document.querySelector(".booking-form");

    if (
        !checkInInput ||
        !checkOutInput ||
        !roomSelect ||
        !guestSelect ||
        !bookingForm
    ) {
        return;
    }

    // Rates and capacities are read from the data attributes the server
    // renders on each room option, so they cannot drift from main.go.
    function selectedRoomOption() {
        return roomSelect.options[roomSelect.selectedIndex];
    }

    function selectedRoomPrice() {
        const option = selectedRoomOption();

        if (!option) {
            return 0;
        }

        return parseInt(option.dataset.price, 10) || 0;
    }

    function selectedRoomCapacity() {
        const option = selectedRoomOption();

        if (!option) {
            return 0;
        }

        return parseInt(option.dataset.capacity, 10) || 0;
    }

    // Create price summary
    const priceSummary = document.createElement("div");

    priceSummary.className = "price-summary";

    priceSummary.innerHTML = `
        <div class="price-row">
            <span>Price per night</span>
            <strong id="price-per-night">₦0</strong>
        </div>

        <div class="price-row">
            <span>Number of nights</span>
            <strong id="number-of-nights">0</strong>
        </div>

        <div class="price-total">
            <span>Estimated total</span>
            <strong id="estimated-total">₦0</strong>
        </div>
    `;

    bookingForm.appendChild(priceSummary);

    const pricePerNightElement =
        document.getElementById("price-per-night");

    const numberOfNightsElement =
        document.getElementById("number-of-nights");

    const estimatedTotalElement =
        document.getElementById("estimated-total");


    function formatCurrency(amount) {
        return new Intl.NumberFormat("en-NG", {
            style: "currency",
            currency: "NGN",
            maximumFractionDigits: 0
        }).format(amount);
    }


    function calculatePrice() {
        const checkInValue = checkInInput.value;
        const checkOutValue = checkOutInput.value;

        const pricePerNight = selectedRoomPrice();

        pricePerNightElement.textContent =
            formatCurrency(pricePerNight);

        if (!checkInValue || !checkOutValue) {
            numberOfNightsElement.textContent = "0";
            estimatedTotalElement.textContent = formatCurrency(0);
            return;
        }

        const checkInDate = new Date(checkInValue + "T00:00:00");
        const checkOutDate = new Date(checkOutValue + "T00:00:00");

        const difference =
            checkOutDate.getTime() - checkInDate.getTime();

        const millisecondsPerDay =
            1000 * 60 * 60 * 24;

        const numberOfNights =
            Math.round(difference / millisecondsPerDay);


        if (numberOfNights <= 0) {
            numberOfNightsElement.textContent = "0";
            estimatedTotalElement.textContent = formatCurrency(0);
            return;
        }

        const estimatedTotal =
            pricePerNight * numberOfNights;

        numberOfNightsElement.textContent =
            numberOfNights;

        estimatedTotalElement.textContent =
            formatCurrency(estimatedTotal);
    }


    checkInInput.addEventListener(
        "change",
        calculatePrice
    );

    checkOutInput.addEventListener(
        "change",
        calculatePrice
    );

    // Keep the guest dropdown within the selected room's capacity so the form
    // cannot offer a party size the server will reject.
    function syncGuestOptions() {
        const capacity = selectedRoomCapacity();

        Array.prototype.forEach.call(
            guestSelect.options,
            function (option) {
                const guests = parseInt(option.value, 10);

                option.disabled = capacity > 0 && guests > capacity;
            }
        );

        const selectedGuests = parseInt(guestSelect.value, 10);

        if (capacity > 0 && selectedGuests > capacity) {
            guestSelect.value = String(capacity);
        }
    }

    roomSelect.addEventListener("change", function () {
        syncGuestOptions();
        calculatePrice();
    });


    // Prevent selecting a checkout date
    // before or equal to the check-in date.
    checkInInput.addEventListener("change", function () {

        if (!checkInInput.value) {
            return;
        }

        checkOutInput.min = checkInInput.value;

        if (
            checkOutInput.value &&
            checkOutInput.value <= checkInInput.value
        ) {
            checkOutInput.value = "";
        }

        calculatePrice();
    });


    // Validate dates before submitting
    bookingForm.addEventListener("submit", function (event) {

        const checkInValue = checkInInput.value;
        const checkOutValue = checkOutInput.value;

        if (!checkInValue || !checkOutValue) {
            return;
        }

        const checkInDate =
            new Date(checkInValue + "T00:00:00");

        const checkOutDate =
            new Date(checkOutValue + "T00:00:00");


        if (checkOutDate <= checkInDate) {

            event.preventDefault();

            alert(
                "Check-out date must be after the check-in date."
            );

            return;
        }

    });


    // "Reserve" on a room card preselects that room. Assigning .value does not
    // fire a change event, so the dependent updates run directly.
    document.querySelectorAll(".reserve-room").forEach(function (link) {

        link.addEventListener("click", function () {

            roomSelect.value = this.dataset.roomId;

            syncGuestOptions();
            calculatePrice();

        });

    });


    // Stays cannot start in the past. Built from the local date so the limit
    // matches the server's own check.
    function localDateValue(date) {
        const year = date.getFullYear();
        const month = String(date.getMonth() + 1).padStart(2, "0");
        const day = String(date.getDate()).padStart(2, "0");

        return year + "-" + month + "-" + day;
    }

    const todayValue = localDateValue(new Date());

    checkInInput.min = todayValue;
    checkOutInput.min = todayValue;


    // Apply the initial guest limits and price
    syncGuestOptions();
    calculatePrice();
});